package segmentadmin

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisStore is a [Store] over go-redis: a single node or a cluster.
type RedisStore struct {
	client redis.UniversalClient
}

var _ Store = (*RedisStore)(nil)

// NewRedis returns a [Store] over a go-redis client. Close closes the client.
func NewRedis(client redis.UniversalClient) *RedisStore { return &RedisStore{client: client} }

// Close implements [Store].
func (s *RedisStore) Close() { _ = s.client.Close() }

// Ping implements [Store].
func (s *RedisStore) Ping(ctx context.Context) error {
	err := s.client.Ping(ctx).Err()
	if err != nil {
		return storeErr("PING", err)
	}

	return nil
}

// SAdd implements [Store].
func (s *RedisStore) SAdd(ctx context.Context, key string, members []string, ttl time.Duration) (int64, error) {
	pipe := s.client.Pipeline()
	add := pipe.SAdd(ctx, key, toAny(members)...)

	if ttl > 0 {
		pipe.PExpire(ctx, key, ttl)
	}

	_, err := pipe.Exec(ctx)
	if err != nil {
		return 0, storeErr("SADD", err)
	}

	return add.Val(), nil
}

func toAny(members []string) []any {
	out := make([]any, len(members))
	for i, member := range members {
		out[i] = member
	}

	return out
}

// SRem implements [Store].
func (s *RedisStore) SRem(ctx context.Context, key string, members []string) (int64, error) {
	removed, err := s.client.SRem(ctx, key, toAny(members)...).Result()
	if err != nil {
		return 0, storeErr("SREM", err)
	}

	return removed, nil
}

// SetKeys implements [Store].
func (s *RedisStore) SetKeys(ctx context.Context, keys []string) (int64, error) {
	pipe := s.client.Pipeline()
	sets := make([]*redis.BoolCmd, len(keys))

	for i, key := range keys {
		sets[i] = pipe.SetNX(ctx, key, "1", 0)
	}

	_, err := pipe.Exec(ctx)
	if err != nil {
		return 0, storeErr("SET", err)
	}

	var created int64

	for _, one := range sets {
		if one.Val() {
			created++
		}
	}

	return created, nil
}

// Swap implements [Store].
func (s *RedisStore) Swap(ctx context.Context, live, tmp, old string, oldTTL time.Duration) error {
	pipe := s.client.TxPipeline()
	first := pipe.Rename(ctx, live, old)
	second := pipe.Rename(ctx, tmp, live)
	persist := pipe.Persist(ctx, live) // RENAME carries the expiry of the temporary key
	pipe.PExpire(ctx, old, oldTTL)

	_, _ = pipe.Exec(ctx) // the commands carry their own errors: a missing live key fails only the first

	err := first.Err()
	if err != nil && !IsNoSuchKey(err) {
		return storeErr("RENAME", err)
	}

	err = second.Err()
	if err != nil {
		return storeErr("RENAME", err)
	}

	err = persist.Err()
	if err != nil {
		return storeErr("PERSIST", err)
	}

	return nil
}

// Unlink implements [Store].
func (s *RedisStore) Unlink(ctx context.Context, keys ...string) (int64, bool, error) {
	var (
		removed int64
		fell    bool
	)

	for _, key := range keys {
		count, err := s.client.Unlink(ctx, key).Result()
		if IsUnknownCommand(err) {
			fell = true
			count, err = s.client.Del(ctx, key).Result()
		}

		if err != nil {
			return removed, fell, storeErr("UNLINK", err)
		}

		removed += count
	}

	return removed, fell, nil
}

// SetValue implements [Store].
func (s *RedisStore) SetValue(ctx context.Context, key, value string) error {
	err := s.client.Set(ctx, key, value, 0).Err()
	if err != nil {
		return storeErr("SET", err)
	}

	return nil
}

// Value implements [Store].
func (s *RedisStore) Value(ctx context.Context, key string) (string, bool, error) {
	val, err := s.client.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return "", false, nil
	}

	if err != nil {
		return "", false, storeErr("GET", err)
	}

	return val, true, nil
}

// SCard implements [Store].
func (s *RedisStore) SCard(ctx context.Context, key string) (int64, error) {
	card, err := s.client.SCard(ctx, key).Result()
	if err != nil {
		return 0, storeErr("SCARD", err)
	}

	return card, nil
}

// KeyTTL implements [Store].
func (s *RedisStore) KeyTTL(ctx context.Context, key string) (bool, time.Duration, error) {
	// go-redis turns the replies -2 and -1 into durations of -2 and -1 nanoseconds
	ttl, err := s.client.PTTL(ctx, key).Result()
	if err != nil {
		return false, 0, storeErr("PTTL", err)
	}

	if ttl == ttlMissing {
		return false, 0, nil
	}

	if ttl == ttlNone {
		return true, 0, nil
	}

	return true, max(ttl, time.Millisecond), nil
}

// CountKeys implements [Store].
func (s *RedisStore) CountKeys(ctx context.Context, prefix string, limit int) (int, bool, error) {
	var (
		mu    sync.Mutex
		total int
		more  bool
	)

	count := func(ctx context.Context, node *redis.Client) error {
		mu.Lock()
		left := limit - total
		mu.Unlock()

		n, over, err := scanRedis(ctx, node, prefix, left)

		mu.Lock()
		total += n
		more = more || over
		mu.Unlock()

		return err
	}

	var err error

	switch client := s.client.(type) {
	case *redis.ClusterClient:
		err = client.ForEachMaster(ctx, count)
	case *redis.Client:
		err = count(ctx, client)
	}

	if err != nil {
		return total, more, storeErr("SCAN", err)
	}

	return min(total, limit), more || total > limit, nil
}

// scanRedis counts the keys of one node, up to limit.
func scanRedis(ctx context.Context, node *redis.Client, prefix string, limit int) (int, bool, error) {
	var (
		cursor uint64
		count  int
	)

	for limit > 0 {
		keys, next, err := node.Scan(ctx, cursor, ScanPattern(prefix), scanCount).Result()
		if err != nil {
			return count, false, storeErr("SCAN", err)
		}

		count += len(keys)
		if count >= limit {
			return limit, count > limit || next != 0, nil
		}

		if next == 0 {
			return count, false, nil
		}

		cursor = next
	}

	return 0, true, nil
}

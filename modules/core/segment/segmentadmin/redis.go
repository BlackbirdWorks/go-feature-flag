package segmentadmin

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisStore is a [Store] over go-redis: a single node or a cluster.
type RedisStore struct {
	client redis.UniversalClient
	stage  *redis.Script
	commit *redis.Script
	unlock *redis.Script
	edit   *redis.Script
}

var _ Store = (*RedisStore)(nil)

// NewRedis returns a [Store] over a go-redis client. Close closes the client.
func NewRedis(client redis.UniversalClient) *RedisStore {
	return &RedisStore{
		client: client, stage: redis.NewScript(stageScript), commit: redis.NewScript(commitScript),
		unlock: redis.NewScript(unlockScript), edit: redis.NewScript(editScript),
	}
}

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

// Lock implements [Store].
func (s *RedisStore) Lock(ctx context.Context, key, token string, ttl time.Duration) (bool, error) {
	ok, err := s.client.SetNX(ctx, key, token, ttl).Result()
	if err != nil {
		return false, storeErr("SET", err)
	}

	return ok, nil
}

// Unlock implements [Store].
func (s *RedisStore) Unlock(ctx context.Context, lock, last, committed, token string) error {
	err := s.unlock.Run(ctx, s.client, []string{lock, last, committed}, token).Err()
	if err != nil {
		return storeErr("EVAL", err)
	}

	return nil
}

// StageAdd implements [Store].
func (s *RedisStore) StageAdd(
	ctx context.Context, lock, tmp, token string, members []string, ttl time.Duration, first bool,
) (int64, StageResult, error) {
	args := append([]any{token, ttl.Milliseconds(), flag(first)}, toAny(members)...)

	added, err := s.stage.Run(ctx, s.client, []string{lock, tmp}, args...).Int64()
	if err != nil {
		return 0, Staged, storeErr("EVAL", err)
	}

	return stageOf(added)
}

// EditSet implements [Store].
func (s *RedisStore) EditSet(
	ctx context.Context, lock, key string, members []string, remove bool,
) (int64, bool, error) {
	args := append([]any{editVerb(remove)}, toAny(members)...)

	changed, err := s.edit.Run(ctx, s.client, []string{lock, key}, args...).Int64()
	if err != nil {
		return 0, false, storeErr("EVAL", err)
	}

	return max(changed, 0), changed < 0, nil
}

// Commit implements [Store].
func (s *RedisStore) Commit(ctx context.Context, commit *Commit) (CommitResult, bool, error) {
	keys := []string{commit.Lock, commit.Tmp, commit.Live, commit.Old, commit.Committed}

	code, err := s.commit.Run(ctx, s.client, keys, commit.Token, commit.Want, commit.OldTTL.Milliseconds(),
		flag(commit.Empty)).Int64()
	if err != nil {
		return LockLost, false, storeErr("EVAL", err)
	}

	result, fell := commitResult(code)

	return result, fell, nil
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
		mu      sync.Mutex
		counter = NewKeyCounter(limit)
	)

	count := func(ctx context.Context, node *redis.Client) error {
		return scanRedis(ctx, node, prefix, counter, &mu)
	}

	var err error

	switch client := s.client.(type) {
	case *redis.ClusterClient:
		err = client.ForEachMaster(ctx, count)
	case *redis.Client:
		err = count(ctx, client)
	}

	if err != nil {
		return counter.Count(), counter.More(), storeErr("SCAN", err)
	}

	return counter.Count(), counter.More(), nil
}

// scanRedis counts the keys of one node.
func scanRedis(ctx context.Context, node *redis.Client, prefix string, counter *KeyCounter, mu *sync.Mutex) error {
	var cursor uint64

	for {
		keys, next, err := node.Scan(ctx, cursor, ScanPattern(prefix), scanCount).Result()
		if err != nil {
			return fmt.Errorf("scan: %w", err)
		}

		mu.Lock()
		full := counter.Add(keys)
		mu.Unlock()

		if full || next == 0 {
			return nil
		}

		cursor = next
	}
}

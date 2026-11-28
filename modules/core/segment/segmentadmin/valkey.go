package segmentadmin

import (
	"context"
	"fmt"
	"time"

	"github.com/valkey-io/valkey-go"
)

// ValkeyStore is a [Store] over valkey-go: a single node, a sentinel set or a cluster.
type ValkeyStore struct {
	client valkey.Client
}

var _ Store = (*ValkeyStore)(nil)

// NewValkey returns a [Store] over a valkey-go client. Close closes the client.
func NewValkey(client valkey.Client) *ValkeyStore { return &ValkeyStore{client: client} }

func storeErr(op string, err error) error {
	return fmt.Errorf("%w: %s: %w", ErrStore, op, err)
}

// Close implements [Store].
func (s *ValkeyStore) Close() { s.client.Close() }

// Ping implements [Store].
func (s *ValkeyStore) Ping(ctx context.Context) error {
	err := s.client.Do(ctx, s.client.B().Ping().Build()).Error()
	if err != nil {
		return storeErr("PING", err)
	}

	return nil
}

// SAdd implements [Store].
func (s *ValkeyStore) SAdd(ctx context.Context, key string, members []string, ttl time.Duration) (int64, error) {
	cmds := valkey.Commands{s.client.B().Sadd().Key(key).Member(members...).Build()}
	if ttl > 0 {
		cmds = append(cmds, s.client.B().Pexpire().Key(key).Milliseconds(ttl.Milliseconds()).Build())
	}

	res := s.client.DoMulti(ctx, cmds...)
	for _, one := range res {
		err := one.Error()
		if err != nil {
			return 0, storeErr("SADD", err)
		}
	}

	added, err := res[0].AsInt64()
	if err != nil {
		return 0, storeErr("SADD", err)
	}

	return added, nil
}

// SRem implements [Store].
func (s *ValkeyStore) SRem(ctx context.Context, key string, members []string) (int64, error) {
	removed, err := s.client.Do(ctx, s.client.B().Srem().Key(key).Member(members...).Build()).AsInt64()
	if err != nil {
		return 0, storeErr("SREM", err)
	}

	return removed, nil
}

// SetKeys implements [Store].
func (s *ValkeyStore) SetKeys(ctx context.Context, keys []string) (int64, error) {
	cmds := make(valkey.Commands, len(keys))
	for i, key := range keys {
		cmds[i] = s.client.B().Set().Key(key).Value("1").Nx().Build()
	}

	var created int64

	for _, one := range s.client.DoMulti(ctx, cmds...) {
		msg, err := one.ToMessage()
		if err != nil && !valkey.IsValkeyNil(err) {
			return created, storeErr("SET", err)
		}

		if err == nil && !msg.IsNil() {
			created++
		}
	}

	return created, nil
}

// Swap implements [Store].
func (s *ValkeyStore) Swap(ctx context.Context, live, tmp, old string, oldTTL time.Duration) error {
	b := s.client.B()
	res := s.client.DoMulti(ctx,
		b.Multi().Build(),
		b.Rename().Key(live).Newkey(old).Build(),
		b.Rename().Key(tmp).Newkey(live).Build(),
		b.Persist().Key(live).Build(), // RENAME carries the expiry of the temporary key
		b.Pexpire().Key(old).Milliseconds(oldTTL.Milliseconds()).Build(),
		b.Exec().Build(),
	)

	arr, err := res[len(res)-1].ToArray()
	if err != nil {
		return storeErr("EXEC", err)
	}

	if len(arr) != swapReplies {
		return storeErr("EXEC", errShortReply)
	}

	first := arr[0].Error()
	if first != nil && !IsNoSuchKey(first) {
		return storeErr("RENAME", first)
	}

	for _, one := range arr[1:3] {
		err = one.Error()
		if err != nil {
			return storeErr("RENAME", err)
		}
	}

	return nil
}

// Unlink implements [Store].
func (s *ValkeyStore) Unlink(ctx context.Context, keys ...string) (int64, bool, error) {
	var (
		removed int64
		fell    bool
	)

	for _, key := range keys {
		count, err := s.client.Do(ctx, s.client.B().Unlink().Key(key).Build()).AsInt64()
		if IsUnknownCommand(err) {
			fell = true
			count, err = s.client.Do(ctx, s.client.B().Del().Key(key).Build()).AsInt64()
		}

		if err != nil {
			return removed, fell, storeErr("UNLINK", err)
		}

		removed += count
	}

	return removed, fell, nil
}

// SetValue implements [Store].
func (s *ValkeyStore) SetValue(ctx context.Context, key, value string) error {
	err := s.client.Do(ctx, s.client.B().Set().Key(key).Value(value).Build()).Error()
	if err != nil {
		return storeErr("SET", err)
	}

	return nil
}

// Value implements [Store].
func (s *ValkeyStore) Value(ctx context.Context, key string) (string, bool, error) {
	val, err := s.client.Do(ctx, s.client.B().Get().Key(key).Build()).ToString()
	if valkey.IsValkeyNil(err) {
		return "", false, nil
	}

	if err != nil {
		return "", false, storeErr("GET", err)
	}

	return val, true, nil
}

// SCard implements [Store].
func (s *ValkeyStore) SCard(ctx context.Context, key string) (int64, error) {
	card, err := s.client.Do(ctx, s.client.B().Scard().Key(key).Build()).AsInt64()
	if err != nil {
		return 0, storeErr("SCARD", err)
	}

	return card, nil
}

// KeyTTL implements [Store].
func (s *ValkeyStore) KeyTTL(ctx context.Context, key string) (bool, time.Duration, error) {
	ms, err := s.client.Do(ctx, s.client.B().Pttl().Key(key).Build()).AsInt64()
	if err != nil {
		return false, 0, storeErr("PTTL", err)
	}

	return pttlOf(ms)
}

// CountKeys implements [Store].
func (s *ValkeyStore) CountKeys(ctx context.Context, prefix string, limit int) (int, bool, error) {
	nodes := []valkey.Client{s.client}

	if s.client.Mode() == valkey.ClientModeCluster {
		nodes = nodes[:0]

		for _, node := range s.client.Nodes() {
			role, err := node.Do(ctx, node.B().Role().Build()).ToArray()
			if err != nil {
				return 0, false, storeErr("ROLE", err)
			}

			if name, _ := role[0].ToString(); name == "master" {
				nodes = append(nodes, node)
			}
		}
	}

	total := 0

	for _, node := range nodes {
		n, more, err := scanNode(ctx, node, prefix, limit-total)
		total += n

		if err != nil || more {
			return total, more, err
		}
	}

	return total, false, nil
}

// scanNode counts the keys of one node, up to limit.
func scanNode(ctx context.Context, node valkey.Client, prefix string, limit int) (int, bool, error) {
	var (
		cursor uint64
		count  int
	)

	for {
		entry, err := node.Do(ctx,
			node.B().Scan().Cursor(cursor).Match(ScanPattern(prefix)).Count(scanCount).Build()).AsScanEntry()
		if err != nil {
			return count, false, storeErr("SCAN", err)
		}

		count += len(entry.Elements)
		if count >= limit {
			return limit, count > limit || entry.Cursor != 0, nil
		}

		cursor = entry.Cursor
		if cursor == 0 {
			return count, false, nil
		}
	}
}

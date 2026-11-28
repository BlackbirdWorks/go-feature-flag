// Package admintest holds an in-memory [segmentadmin.Store] with failure injection, for tests of the code that
// writes segments (the import and the commands of the CLI) that must not need a server.
package admintest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/blackbirdworks/semaphore/modules/core/segment/segmentadmin"
)

// ErrInjected is the failure that [Fake.Fail] returns.
var ErrInjected = errors.New("injected failure")

// ErrNoSuchKey is the failure of a swap whose temporary key does not exist.
var ErrNoSuchKey = errors.New("ERR no such key")

// ErrCrossSlot is the failure of a swap whose keys are not in one cluster slot.
var ErrCrossSlot = errors.New("CROSSSLOT keys in request don't hash to the same slot")

// Fake is an in-memory store. It is safe for concurrent use. It records every call as "<OP> <key>" (never a member).
type Fake struct {
	sets     map[string]map[string]struct{}
	strs     map[string]string
	ttls     map[string]time.Duration
	failures map[string]int
	calls    []string
	mu       sync.Mutex
	noUnlink bool
}

var _ segmentadmin.Store = (*Fake)(nil)

// New returns an empty store.
func New() *Fake {
	return &Fake{
		sets: map[string]map[string]struct{}{}, strs: map[string]string{}, ttls: map[string]time.Duration{},
		failures: map[string]int{},
	}
}

// Fail makes the nth next call of an operation ("SADD", "SWAP", "UNLINK", "SET" ...) fail with [ErrInjected]; n is
// 1 for the next call.
func (f *Fake) Fail(op string, nth int) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.failures[op] = nth
}

// WithoutUnlink makes UNLINK an unknown command, as on a server that predates it.
func (f *Fake) WithoutUnlink() { f.noUnlink = true }

// Calls returns the calls so far, in order.
func (f *Fake) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]string(nil), f.calls...)
}

// Members returns the sorted members of a set.
func (f *Fake) Members(key string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make([]string, 0, len(f.sets[key]))
	for member := range f.sets[key] {
		out = append(out, member)
	}

	slices.Sort(out)

	return out
}

// Keys returns the sorted names of every key.
func (f *Fake) Keys() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make([]string, 0, len(f.sets)+len(f.strs))
	for key := range f.sets {
		out = append(out, key)
	}

	for key := range f.strs {
		out = append(out, key)
	}

	slices.Sort(out)

	return out
}

// TTLOf returns the expiry that was set on a key, zero for none.
func (f *Fake) TTLOf(key string) time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.ttls[key]
}

// Close does nothing.
func (*Fake) Close() {}

// Ping implements [segmentadmin.Store].
func (f *Fake) Ping(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.enter(ctx, "PING", "")
}

// SAdd implements [segmentadmin.Store].
func (f *Fake) SAdd(ctx context.Context, key string, members []string, ttl time.Duration) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	err := f.enter(ctx, "SADD", key)
	if err != nil {
		return 0, err
	}

	set := f.sets[key]
	if set == nil {
		set = map[string]struct{}{}
		f.sets[key] = set
	}

	var added int64

	for _, member := range members {
		if _, ok := set[member]; !ok {
			set[member] = struct{}{}
			added++
		}
	}

	if ttl > 0 {
		f.ttls[key] = ttl
	}

	return added, nil
}

// SRem implements [segmentadmin.Store].
func (f *Fake) SRem(ctx context.Context, key string, members []string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	err := f.enter(ctx, "SREM", key)
	if err != nil {
		return 0, err
	}

	var removed int64

	for _, member := range members {
		if _, ok := f.sets[key][member]; ok {
			delete(f.sets[key], member)

			removed++
		}
	}

	if len(f.sets[key]) == 0 {
		delete(f.sets, key)
	}

	return removed, nil
}

// SetKeys implements [segmentadmin.Store].
func (f *Fake) SetKeys(ctx context.Context, keys []string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	err := f.enter(ctx, "SETKEYS", "")
	if err != nil {
		return 0, err
	}

	var created int64

	for _, key := range keys {
		if _, ok := f.strs[key]; !ok {
			f.strs[key] = "1"
			created++
		}
	}

	return created, nil
}

// Swap implements [segmentadmin.Store]: it fails when the keys are not in one slot, as a cluster does.
func (f *Fake) Swap(ctx context.Context, live, tmp, old string, oldTTL time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	err := f.enter(ctx, "SWAP", live)
	if err != nil {
		return err
	}

	if segmentadmin.Slot(live) != segmentadmin.Slot(tmp) || segmentadmin.Slot(live) != segmentadmin.Slot(old) {
		return ErrCrossSlot
	}

	if set, ok := f.sets[live]; ok {
		f.sets[old] = set
		f.ttls[old] = oldTTL
	}

	delete(f.sets, live)

	set, ok := f.sets[tmp]
	if !ok {
		return ErrNoSuchKey // the live key has been moved already: a transaction does not roll back
	}

	f.sets[live] = set
	delete(f.sets, tmp)
	delete(f.ttls, tmp) // the expiry is dropped by PERSIST

	return nil
}

// Unlink implements [segmentadmin.Store].
func (f *Fake) Unlink(ctx context.Context, keys ...string) (int64, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var removed int64

	for _, key := range keys {
		err := f.enter(ctx, "UNLINK", key)
		if err != nil {
			return removed, false, err
		}

		if _, ok := f.sets[key]; ok {
			removed++
		}

		if _, ok := f.strs[key]; ok {
			removed++
		}

		delete(f.sets, key)
		delete(f.strs, key)
		delete(f.ttls, key)
	}

	return removed, f.noUnlink, nil
}

// SetValue implements [segmentadmin.Store].
func (f *Fake) SetValue(ctx context.Context, key, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	err := f.enter(ctx, "SET", key)
	if err != nil {
		return err
	}

	f.strs[key] = value

	return nil
}

// Value implements [segmentadmin.Store].
func (f *Fake) Value(ctx context.Context, key string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	err := f.enter(ctx, "GET", key)
	if err != nil {
		return "", false, err
	}

	val, ok := f.strs[key]

	return val, ok, nil
}

// SCard implements [segmentadmin.Store].
func (f *Fake) SCard(ctx context.Context, key string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	err := f.enter(ctx, "SCARD", key)
	if err != nil {
		return 0, err
	}

	return int64(len(f.sets[key])), nil
}

// KeyTTL implements [segmentadmin.Store].
func (f *Fake) KeyTTL(ctx context.Context, key string) (bool, time.Duration, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	err := f.enter(ctx, "PTTL", key)
	if err != nil {
		return false, 0, err
	}

	_, set := f.sets[key]

	return set, f.ttls[key], nil
}

// CountKeys implements [segmentadmin.Store].
func (f *Fake) CountKeys(ctx context.Context, prefix string, limit int) (int, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	err := f.enter(ctx, "SCAN", prefix)
	if err != nil {
		return 0, false, err
	}

	count := 0

	for key := range f.strs {
		if strings.HasPrefix(key, prefix) {
			count++
		}
	}

	if count > limit {
		return limit, true, nil
	}

	return count, false, nil
}

// enter records a call and returns the injected failure or the end of the context. The caller holds the lock.
func (f *Fake) enter(ctx context.Context, command, key string) error {
	f.calls = append(f.calls, command+" "+key)

	err := ctx.Err()
	if err != nil {
		return fmt.Errorf("fake store: %w", err)
	}

	if left := f.failures[command]; left > 0 {
		f.failures[command] = left - 1
		if left == 1 {
			return fmt.Errorf("%w: %s: %w", segmentadmin.ErrStore, command, ErrInjected)
		}
	}

	return nil
}

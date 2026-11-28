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

// ErrLostReply is the failure of a call that was applied and whose reply was lost.
var ErrLostReply = errors.New("the reply was lost")

// ErrCrossSlot is the failure of a swap whose keys are not in one cluster slot.
var ErrCrossSlot = errors.New("CROSSSLOT keys in request don't hash to the same slot")

// Fake is an in-memory store. It is safe for concurrent use. It records every call as "<OP> <key>" (never a member).
type Fake struct {
	sets     map[string]map[string]struct{}
	strs     map[string]string
	ttls     map[string]time.Duration
	failures map[string]int
	failFrom map[string]int
	lost     map[string]int
	lostAll  map[string]bool
	seen     map[string]int
	calls    []string
	mu       sync.Mutex
	noUnlink bool
}

var _ segmentadmin.Store = (*Fake)(nil)

// New returns an empty store.
func New() *Fake {
	return &Fake{
		sets: map[string]map[string]struct{}{}, strs: map[string]string{}, ttls: map[string]time.Duration{},
		failures: map[string]int{}, failFrom: map[string]int{}, lost: map[string]int{}, lostAll: map[string]bool{},
		seen: map[string]int{},
	}
}

// Fail makes the nth next call of an operation ("SADD", "SWAP", "UNLINK", "SET" ...) fail with [ErrInjected]; n is
// 1 for the next call.
func (f *Fake) Fail(op string, nth int) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.failures[op] = nth
}

// FailFrom makes every call of an operation from the nth on fail with [ErrInjected], as a store that stays down does.
func (f *Fake) FailFrom(op string, nth int) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.failFrom[op] = f.seen[op] + nth
}

// LoseReply makes the nth call of an operation ("SADD" of a batch, "SWAP", "EDIT") apply and then fail with
// [ErrLostReply], as a call whose reply never came back. With always, every call from the nth on does.
func (f *Fake) LoseReply(op string, nth int, always bool) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.lost[op], f.lostAll[op] = f.seen[op]+nth, always
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

// Lock implements [segmentadmin.Store]: SET NX.
func (f *Fake) Lock(ctx context.Context, key, token string, ttl time.Duration) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	err := f.enter(ctx, "LOCK", key)
	if err != nil {
		return false, err
	}

	if _, held := f.strs[key]; held {
		return false, nil
	}

	f.strs[key] = token
	f.ttls[key] = ttl

	return true, nil
}

// Unlock implements [segmentadmin.Store]: the compare and delete.
func (f *Fake) Unlock(ctx context.Context, lock, last, committed, token string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	err := f.enter(ctx, "UNLOCK", lock)
	if err != nil {
		return err
	}

	if f.strs[lock] == token {
		delete(f.strs, lock)
		delete(f.strs, last)
		delete(f.ttls, lock)
	}

	if f.strs[committed] == token {
		delete(f.strs, committed)
	}

	return nil
}

// StageAdd implements [segmentadmin.Store]: the batch is written only while the lock holds the token, and only the
// first batch may create the temporary key.
func (f *Fake) StageAdd(
	ctx context.Context, lock, tmp, token string, members []string, ttl time.Duration, first bool,
) (int64, segmentadmin.StageResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	err := f.enter(ctx, "SADD", tmp)
	if err != nil {
		return 0, segmentadmin.Staged, err
	}

	if f.strs[lock] != token {
		return 0, segmentadmin.StageLockLost, nil
	}

	set := f.sets[tmp]
	if set == nil && !first {
		return 0, segmentadmin.StageTempGone, nil
	}

	if set == nil {
		set = map[string]struct{}{}
		f.sets[tmp] = set
	}

	var added int64

	for _, member := range members {
		if _, ok := set[member]; !ok {
			set[member] = struct{}{}
			added++
		}
	}

	f.ttls[tmp], f.ttls[lock] = ttl, ttl

	return added, segmentadmin.Staged, f.lose("SADD")
}

// EditSet implements [segmentadmin.Store]: refused while the import lock exists.
func (f *Fake) EditSet(ctx context.Context, lock, key string, members []string, remove bool) (int64, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	err := f.enter(ctx, "EDIT", key)
	if err != nil {
		return 0, false, err
	}

	if _, held := f.strs[lock]; held {
		return 0, true, nil
	}

	set := f.sets[key]
	if set == nil && !remove {
		set = map[string]struct{}{}
		f.sets[key] = set
	}

	var changed int64

	for _, member := range members {
		_, present := set[member]

		switch {
		case remove && present:
			delete(set, member)

			changed++
		case !remove && !present:
			set[member] = struct{}{}

			changed++
		}
	}

	if len(set) == 0 {
		delete(f.sets, key)
	}

	return changed, false, f.lose("EDIT")
}

// Commit implements [segmentadmin.Store]: the conditional swap, in one step as the script does it.
func (f *Fake) Commit(ctx context.Context, commit *segmentadmin.Commit) (segmentadmin.CommitResult, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	err := f.enter(ctx, "SWAP", commit.Live)
	if err != nil {
		return segmentadmin.LockLost, false, err
	}

	result, fell, err := f.commitLocked(commit)
	if err != nil {
		return result, fell, err
	}

	return result, fell, f.lose("SWAP")
}

// Delete removes keys without a command and without a record, as an expiry would.
func (f *Fake) Delete(keys ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, key := range keys {
		delete(f.sets, key)
		delete(f.strs, key)
		delete(f.ttls, key)
	}
}

// Put sets a string key without a command and without a record: another run that took a lock, say.
func (f *Fake) Put(key, value string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.strs[key] = value
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

	f.seen[command]++

	if from := f.failFrom[command]; from > 0 && f.seen[command] >= from {
		return fmt.Errorf("%w: %s: %w", segmentadmin.ErrStore, command, ErrInjected)
	}

	if left := f.failures[command]; left > 0 {
		f.failures[command] = left - 1
		if left == 1 {
			return fmt.Errorf("%w: %s: %w", segmentadmin.ErrStore, command, ErrInjected)
		}
	}

	return nil
}

// lose tells whether the reply of the call that just ran is lost. The caller holds the lock and has applied the call.
func (f *Fake) lose(command string) error {
	nth := f.lost[command]
	if nth == 0 || f.seen[command] < nth || (!f.lostAll[command] && f.seen[command] > nth) {
		return nil
	}

	return fmt.Errorf("%w: %s: %w", segmentadmin.ErrStore, command, ErrLostReply)
}

// commitLocked applies the swap. The caller holds the lock.
func (f *Fake) commitLocked(commit *segmentadmin.Commit) (segmentadmin.CommitResult, bool, error) {
	for _, key := range []string{commit.Tmp, commit.Old, commit.Lock, commit.Committed} {
		if segmentadmin.Slot(key) != segmentadmin.Slot(commit.Live) {
			return segmentadmin.LockLost, false, ErrCrossSlot
		}
	}

	switch {
	case f.strs[commit.Committed] == commit.Token:
		return segmentadmin.AlreadyCommitted, false, nil
	case f.strs[commit.Lock] != commit.Token:
		return segmentadmin.LockLost, false, nil
	case commit.Empty:
		delete(f.sets, commit.Live)
		delete(f.ttls, commit.Live)
		f.strs[commit.Committed] = commit.Token

		return segmentadmin.Committed, f.noUnlink, nil
	}

	set, ok := f.sets[commit.Tmp]

	switch {
	case !ok:
		return segmentadmin.TempGone, false, nil
	case int64(len(set)) < commit.Want:
		return segmentadmin.CountMismatch, false, nil
	}

	if live, exists := f.sets[commit.Live]; exists {
		f.sets[commit.Old] = live
		f.ttls[commit.Old] = commit.OldTTL
	}

	f.sets[commit.Live] = set
	delete(f.sets, commit.Tmp)
	delete(f.ttls, commit.Tmp)
	delete(f.ttls, commit.Live) // PERSIST

	f.strs[commit.Committed] = commit.Token

	return segmentadmin.Committed, false, nil
}

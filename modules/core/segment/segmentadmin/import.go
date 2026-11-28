package segmentadmin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"

	"github.com/blackbirdworks/semaphore/modules/core/clock"
	"github.com/blackbirdworks/semaphore/modules/core/segment/redissegment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/resolver"
)

// The bounds of an import.
const (
	// DefaultBatchSize is the members of one SADD when ImportOptions.BatchSize is zero.
	DefaultBatchSize = 1000
	// MaxBatchSize is the most members of one SADD: a batch is one command that the server runs alone.
	MaxBatchSize = 100_000
	// DefaultTempTTL is the life of the temporary key when ImportOptions.TempTTL is zero.
	DefaultTempTTL = time.Hour
	// cleanupTimeout bounds the removal of the temporary key and the release of the lock after a failure.
	cleanupTimeout = 10 * time.Second
	// minTempTTL is the shortest ttl of the temporary key: PEXPIRE with 0 deletes the key.
	minTempTTL = time.Second
	// tries is how many times a step whose reply may be lost is sent.
	tries = 3
	// retryDelay is the pause before the second try; each next one doubles it.
	retryDelay = 100 * time.Millisecond
	// DelWarnMembers is the size of a replaced set above which a DEL (the fallback of a server without UNLINK) may
	// block the server for a noticeable time, and the report says so.
	DelWarnMembers = 100_000
	tokenBytes     = 16
)

// Target is a segment as its definition and the bootstrap file say it: the operator does not retype any of it.
type Target struct {
	// Name is the name of the segment (for messages).
	Name string
	// Key is the key of the set, or the prefix of the member keys.
	Key string
	// Layout is redissegment.LayoutSet (or empty) or redissegment.LayoutKeys.
	Layout redissegment.Layout
	// VersionKey is the key that the relay polls for a change, empty for none.
	VersionKey string
	// Normalize is the normalization of the ids that the relay applies before a lookup.
	Normalize resolver.Normalization
	// MaxIDBytes is the longest id that the relay looks up; a longer one is rejected. Zero means no bound.
	MaxIDBytes int
}

// ImportOptions set how an import runs.
type ImportOptions struct {
	Wait        func(ctx context.Context, delay time.Duration) error
	Token       string
	Fingerprint string
	BatchSize   int
	TempTTL     time.Duration
	AllowEmpty  bool
	DryRun      bool
}

// Report is what an import did. It holds counts only, never an id.
type Report struct {
	// Version is the value written to the version key, empty when none was.
	Version string
	// Rejected counts the lines that are not an id, by reason.
	Rejected Rejections
	// Read is the lines read.
	Read int64
	// Applied is the ids that an add or a remove sent to the store, whatever they changed.
	Applied int64
	// Accepted is the lines that are an id.
	Accepted int64
	// Added is the distinct ids written (zero for a dry run).
	Added int64
	// Removed is the ids that a remove took out of the segment.
	Removed int64
	// Duplicates is the accepted ids that were already in the input (zero for a dry run, and not exact when
	// DuplicatesUnknown).
	Duplicates int64
	// Swapped tells that the new set is live.
	Swapped bool
	// DuplicatesUnknown tells that a batch was sent again after a lost reply, so that the count of new members, and
	// the duplicates that follow from it, are not exact.
	DuplicatesUnknown bool
	// FallbackLarge tells that DEL (no UNLINK on the server) removed a set above [DelWarnMembers]: it may have blocked.
	FallbackLarge bool
	// CleanupFailed tells that the temporary key could not be removed after a failure: it expires.
	CleanupFailed bool
	// FellBackToDel tells that the server has no UNLINK and DEL removed the old set.
	FellBackToDel bool
	// DryRun tells that nothing was written.
	DryRun bool
}

func (opts *ImportOptions) fill() error {
	if opts.BatchSize == 0 {
		opts.BatchSize = DefaultBatchSize
	}

	if opts.BatchSize < 0 || opts.BatchSize > MaxBatchSize {
		return fmt.Errorf("%w: batch size must be 1 to %d", ErrBadOptions, MaxBatchSize)
	}

	if opts.TempTTL == 0 {
		opts.TempTTL = DefaultTempTTL
	}

	if opts.TempTTL < minTempTTL {
		return fmt.Errorf("%w: the ttl of the temporary key must be %s or more", ErrBadOptions, minTempTTL)
	}

	if opts.Wait == nil {
		opts.Wait = waitTimer
	}

	return nil
}

// importRun is the state of one import.
type importRun struct {
	store  Store
	target *Target
	rep    *Report
	keys   RunKeys
	token  string
	batch  []string
	opts   ImportOptions
	locked bool
	staged bool
	maybe  bool
}

// Import loads the ids of src into the set of the segment and swaps it in. It is safe by construction:
//
//   - One import of a segment runs at a time: a lock key takes the token of the run (SET NX PX). A second run fails at
//     once with [ErrImportRunning]. The lock and the temporary key are refreshed by every batch, in a script that
//     checks the token, and removed at the end only if the token is still the run's.
//   - The temporary key has the token in its name, so nothing else can share or recreate it. The key records its name
//     in a last key, so that the next run removes exactly the key of a dead run, without a scan.
//   - The swap is one script on the server: it moves the live key only if the lock holds the token, the temporary
//     key exists and holds exactly the number of members that the run wrote. Otherwise nothing changes.
//
// The order is: fill, swap, bump the version key, remove the old set without blocking.
func Import(ctx context.Context, store Store, target *Target, src Source, opts ImportOptions) (*Report, error) {
	err := opts.fill()
	if err != nil {
		return nil, err
	}

	if target.Layout == redissegment.LayoutKeys {
		return nil, ErrKeysLayout
	}

	run := &importRun{
		store: store, target: target, opts: opts, rep: &Report{DryRun: opts.DryRun}, token: opts.Token,
	}

	if run.token == "" {
		run.token, err = newToken()
		if err != nil {
			return nil, err
		}
	}

	run.keys, err = ImportKeys(target.Key, run.token)
	if err != nil {
		return nil, err
	}

	if !opts.DryRun {
		err = run.claim(ctx)
		if err != nil {
			return run.rep, err
		}
	}

	err = run.fill(ctx, src)
	if err == nil {
		err = run.finish(ctx)
	}

	run.release(ctx, err)

	return run.rep, err
}

// waitTimer waits for delay, or for the end of ctx.
func waitTimer(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("wait: %w", ctx.Err())
	}
}

func newToken() (string, error) {
	raw := make([]byte, tokenBytes)

	_, err := rand.Read(raw)
	if err != nil {
		return "", fmt.Errorf("make a token: %w", err)
	}

	return hex.EncodeToString(raw), nil
}

// claim takes the lock and removes the temporary key that the previous holder named, if it left one.
func (run *importRun) claim(ctx context.Context) error {
	held, err := run.lock(ctx)
	if err != nil {
		run.locked = true // it may have been taken: release it by token
		run.release(ctx, err)

		return fmt.Errorf("take the lock: %w", err)
	}

	if !held {
		return ErrImportRunning
	}

	run.locked = true

	prev, found, err := run.store.Value(ctx, run.keys.Last)
	if err != nil {
		return run.fail(ctx, fmt.Errorf("read the last temporary key: %w", err))
	}

	stale := ""
	if found && run.keys.Owns(prev) && prev != run.keys.Tmp {
		stale = prev
	}

	// the removal and the record are one script that checks that the lock is still the run's
	owned, err := run.store.Claim(ctx, run.keys.Lock, run.keys.Last, stale, run.keys.Tmp, run.token)
	if err != nil {
		return run.fail(ctx, fmt.Errorf("record the temporary key: %w", err))
	}

	if !owned {
		return run.fail(ctx, ErrLockLost)
	}

	return nil
}

// lock takes the lock. The call is idempotent by the token, so it is sent again when its reply is lost.
func (run *importRun) lock(ctx context.Context) (bool, error) {
	var (
		held bool
		err  error
	)

	for try := range tries {
		held, err = run.store.Lock(ctx, run.keys.Lock, run.token, run.opts.TempTTL)
		if err == nil || ctx.Err() != nil || try == tries-1 {
			break
		}

		if run.opts.Wait(ctx, retryDelay<<try) != nil {
			break
		}
	}

	if err != nil {
		return held, fmt.Errorf("lock: %w", err)
	}

	return held, nil
}

func (run *importRun) fail(ctx context.Context, err error) error {
	run.release(ctx, err)

	return err
}

// release removes the temporary key after a failure (its name is the run's own, so it is never another run's key) and
// the lock if the token is still the run's, on a context of its own: the one of the run may be the failure.
func (run *importRun) release(ctx context.Context, failure error) {
	if !run.locked {
		return
	}

	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()

	if failure != nil && !errors.Is(failure, ErrSwapped) {
		_, _, err := run.store.Unlink(cctx, run.keys.Tmp)
		run.rep.CleanupFailed = err != nil // the ttl removes the key
	}

	err := run.store.Unlock(cctx, run.keys.Lock, run.keys.Last, run.keys.Committed, run.token)
	run.rep.CleanupFailed = run.rep.CleanupFailed || err != nil // the ttl removes the lock
	run.locked = false
}

// fill reads the input and writes its ids to the temporary key in batches.
func (run *importRun) fill(ctx context.Context, src Source) error {
	for {
		err := ctx.Err()
		if err != nil {
			return fmt.Errorf("import stopped: %w", err)
		}

		rec, err := src.Next()
		if errors.Is(err, io.EOF) {
			return run.flush(ctx)
		}

		if err != nil {
			return fmt.Errorf("read the input: %w", err)
		}

		run.rep.Read++

		id, reason := run.accept(&rec)
		if reason != "" {
			run.rep.Rejected.Add(reason)

			continue
		}

		run.rep.Accepted++
		run.batch = append(run.batch, id)

		if len(run.batch) >= run.opts.BatchSize {
			err = run.flush(ctx)
			if err != nil {
				return err
			}
		}
	}
}

// accept normalises a record as the relay does before a lookup. An id with a control character (a line break, for one)
// is rejected, whether it came from a file or from the command line.
func (run *importRun) accept(rec *Record) (string, Reason) {
	if rec.Reason != "" {
		return "", rec.Reason
	}

	id, ok := resolver.NormalizeID(run.target.Normalize, rec.ID)
	if !ok {
		return "", ReasonInvalid
	}

	switch {
	case id == "":
		return "", ReasonEmpty
	case run.target.MaxIDBytes > 0 && len(id) > run.target.MaxIDBytes:
		return "", ReasonTooLong
	case strings.ContainsFunc(id, unicode.IsControl):
		return "", ReasonInvalid
	}

	return id, ""
}

func (run *importRun) flush(ctx context.Context) error {
	if len(run.batch) == 0 || run.opts.DryRun {
		run.batch = run.batch[:0]

		return nil
	}

	added, result, err := run.stage(ctx)
	if err != nil {
		return fmt.Errorf("write a batch: %w", err)
	}

	switch result {
	case Staged:
	case StageLockLost:
		return ErrLockLost
	case StageTempGone:
		return ErrTempGone
	}

	run.staged = true
	run.rep.Added += added
	run.batch = run.batch[:0]

	return nil
}

// stage sends the batch. A batch is a set insertion, so it can be sent again when its reply is lost: the members that
// the first send added are not counted again (Added may be low), and the swap asks for at least that many.
func (run *importRun) stage(ctx context.Context) (int64, StageResult, error) {
	var (
		added  int64
		result StageResult
		err    error
	)

	for try := range tries {
		added, result, err = run.store.StageAdd(
			ctx, run.keys.Lock, run.keys.Tmp, run.token, run.batch, run.opts.TempTTL, !run.staged)
		if err == nil || ctx.Err() != nil || try == tries-1 {
			break
		}

		run.rep.DuplicatesUnknown = true

		if run.opts.Wait(ctx, retryDelay<<try) != nil {
			break
		}
	}

	if err != nil {
		return added, result, fmt.Errorf("stage: %w", err)
	}

	return added, result, nil
}

// finish swaps the new set in, bumps the version and removes the old set.
func (run *importRun) finish(ctx context.Context) error {
	if run.rep.Accepted == 0 && !run.opts.AllowEmpty {
		return ErrEmptyInput
	}

	if run.opts.DryRun {
		return nil
	}

	err := ctx.Err()
	if err != nil {
		return fmt.Errorf("import stopped: %w", err)
	}

	run.rep.Duplicates = run.rep.Accepted - run.rep.Added

	result, fell, err := run.commit(ctx)
	if err != nil {
		return err
	}

	switch result {
	case Committed, AlreadyCommitted:
	case LockLost:
		return ErrLockLost
	case TempGone:
		return ErrTempGone
	case CountMismatch:
		return ErrIncomplete
	}

	run.rep.Swapped = true
	run.rep.FellBackToDel = fell

	// the swap is applied: the bump and the removal run on a context of their own, whatever became of the run's
	bctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()

	err = run.bump(bctx)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrSwapped, err)
	}

	return run.removeOld(bctx)
}

// commit sends the swap. The swap is idempotent by the token of the run, so when the reply is lost it is sent again
// (on a context of its own: the run may have been stopped after the first send) until an answer comes. No answer
// leaves the state unknown, and the error says so.
func (run *importRun) commit(ctx context.Context) (CommitResult, bool, error) {
	commit := &Commit{
		Lock: run.keys.Lock, Tmp: run.keys.Tmp, Live: run.target.Key, Old: run.keys.Old,
		Committed: run.keys.Committed, Source: run.keys.Source, Fingerprint: run.opts.Fingerprint,
		Token: run.token, Want: run.rep.Added, OldTTL: run.opts.TempTTL, Empty: run.rep.Accepted == 0,
	}

	var err error

	for try := range tries {
		attempt, cancel := ctx, context.CancelFunc(func() {})
		if try > 0 {
			attempt, cancel = context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		}

		result, fell, sendErr := run.store.Commit(attempt, commit)

		cancel()

		if sendErr == nil {
			return result, fell, nil
		}

		err = sendErr

		if try < tries-1 {
			_ = run.opts.Wait(context.WithoutCancel(ctx), retryDelay<<try)
		}
	}

	return LockLost, false, fmt.Errorf("%w: %w", ErrSwapUnknown, err)
}

// removeOld removes the replaced set without blocking, and tells when the fallback to DEL could have blocked.
func (run *importRun) removeOld(ctx context.Context) error {
	if run.rep.Accepted == 0 {
		return nil // the explicit empty import: there is no old set to move
	}

	size, err := run.store.SCard(ctx, run.keys.Old)
	if err != nil {
		return fmt.Errorf("%w: read the old set (it expires): %w", ErrSwapped, err)
	}

	_, fell, err := run.store.Unlink(ctx, run.keys.Old)
	if err != nil {
		return fmt.Errorf("%w: remove the old set (it expires): %w", ErrSwapped, err)
	}

	run.rep.FellBackToDel = run.rep.FellBackToDel || fell
	run.rep.FallbackLarge = fell && size > DelWarnMembers

	return nil
}

// bump changes the version key, after the swap, so that a relay that sees the new version finds the new set.
func (run *importRun) bump(ctx context.Context) error {
	return BumpWithRetry(ctx, run.store, run.target, run.rep, run.opts.Wait)
}

// Bump writes a new version to the version key of the target, when it has one, and records it in rep.
func Bump(ctx context.Context, store Store, target *Target, rep *Report) error {
	if target.VersionKey == "" {
		return nil
	}

	version := clock.Now().UTC().Format(time.RFC3339Nano)

	err := store.SetValue(ctx, target.VersionKey, version)
	if err != nil {
		return fmt.Errorf("bump the version key: %w", err)
	}

	rep.Version = version

	return nil
}

// BumpWithRetry is [Bump] with three tries and a pause between them (wait nil means a timer). When it gives up, the
// error is [ErrNotBumped]: the change is in the store and the relays have not been told.
func BumpWithRetry(
	ctx context.Context, store Store, target *Target, rep *Report, wait func(context.Context, time.Duration) error,
) error {
	if wait == nil {
		wait = waitTimer
	}

	var err error

	for try := range tries {
		err = Bump(ctx, store, target, rep)
		if err == nil {
			return nil
		}

		if try < tries-1 && wait(ctx, retryDelay<<try) != nil {
			break
		}
	}

	return fmt.Errorf("%w: %w", ErrNotBumped, err)
}

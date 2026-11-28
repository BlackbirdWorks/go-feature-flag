package segmentadmin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

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
	// cleanupTimeout bounds the removal of the temporary key after a failure.
	cleanupTimeout = 10 * time.Second
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
	// BatchSize is the members of one SADD. Zero means [DefaultBatchSize].
	BatchSize int
	// TempTTL is the life of the temporary key, refreshed by every batch. Zero means [DefaultTempTTL].
	TempTTL time.Duration
	// AllowEmpty lets an input without a valid id empty the segment.
	AllowEmpty bool
	// DryRun reads and validates the input and writes nothing.
	DryRun bool
}

// Report is what an import did. It holds counts only, never an id.
type Report struct {
	// Version is the value written to the version key, empty when none was.
	Version string
	// Rejected counts the lines that are not an id, by reason.
	Rejected Rejections
	// Read is the lines read.
	Read int64
	// Accepted is the lines that are an id.
	Accepted int64
	// Added is the distinct ids written (zero for a dry run).
	Added int64
	// Removed is the ids that a remove took out of the segment.
	Removed int64
	// Duplicates is the accepted ids that were already in the input (zero for a dry run).
	Duplicates int64
	// Swapped tells that the new set is live.
	Swapped bool
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

	if opts.TempTTL < 0 {
		return fmt.Errorf("%w: the temporary key needs a positive ttl", ErrBadOptions)
	}

	return nil
}

// importRun is the state of one import.
type importRun struct {
	store  Store
	target *Target
	rep    *Report
	tmp    string
	old    string
	batch  []string
	opts   ImportOptions
}

// Import loads the ids of src into the set of the segment and swaps it in. The live set is untouched until the swap,
// which is one MULTI/EXEC: a reader sees the whole old set or the whole new set. The order is: fill the temporary key
// (in the slot of the live key, with a ttl that every batch refreshes), swap, bump the version key, remove the old
// set without blocking. A failure or a cancelled context before the swap removes the temporary key and leaves the live
// set as it was; a key that a killed run left expires, and the next run removes it.
func Import(ctx context.Context, store Store, target *Target, src *Reader, opts ImportOptions) (*Report, error) {
	err := opts.fill()
	if err != nil {
		return nil, err
	}

	if target.Layout == redissegment.LayoutKeys {
		return nil, ErrKeysLayout
	}

	run := &importRun{
		store: store, target: target, opts: opts, rep: &Report{DryRun: opts.DryRun},
	}

	run.tmp, run.old, err = TempKeys(target.Key)
	if err != nil {
		return nil, err
	}

	if !opts.DryRun {
		// a killed run of this segment left its keys: they are removed, whatever the state
		_, _, err = store.Unlink(ctx, run.tmp, run.old)
		if err != nil {
			return nil, fmt.Errorf("remove a previous temporary key: %w", err)
		}
	}

	err = run.fill(ctx, src)
	if err == nil {
		err = run.finish(ctx)
	}

	if err != nil {
		if !errors.Is(err, ErrSwapped) && !opts.DryRun {
			run.cleanup(ctx)
		}

		return run.rep, err
	}

	return run.rep, nil
}

// cleanup removes the temporary key after a failure, on a context of its own: the one of the run may be the failure.
func (run *importRun) cleanup(ctx context.Context) {
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()

	_, _, err := run.store.Unlink(cctx, run.tmp)
	run.rep.CleanupFailed = err != nil // the ttl removes the key
}

// fill reads the input and writes its ids to the temporary key in batches.
func (run *importRun) fill(ctx context.Context, src *Reader) error {
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
			return err
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

// accept normalises a record as the relay does before a lookup.
func (run *importRun) accept(rec *Record) (string, Reason) {
	if rec.Reason != "" {
		return "", rec.Reason
	}

	id, ok := resolver.NormalizeID(run.target.Normalize, rec.ID)
	if !ok {
		return "", ReasonInvalid
	}

	if id == "" {
		return "", ReasonEmpty
	}

	if run.target.MaxIDBytes > 0 && len(id) > run.target.MaxIDBytes {
		return "", ReasonTooLong
	}

	return id, ""
}

func (run *importRun) flush(ctx context.Context) error {
	if len(run.batch) == 0 || run.opts.DryRun {
		run.batch = run.batch[:0]

		return nil
	}

	added, err := run.store.SAdd(ctx, run.tmp, run.batch, run.opts.TempTTL)
	if err != nil {
		return fmt.Errorf("write a batch: %w", err)
	}

	run.rep.Added += added
	run.batch = run.batch[:0]

	return nil
}

// finish swaps the new set in, bumps the version and removes the old set.
func (run *importRun) finish(ctx context.Context) error {
	if run.rep.Accepted == 0 && !run.opts.AllowEmpty {
		return ErrEmptyInput
	}

	if run.opts.DryRun {
		return nil
	}

	run.rep.Duplicates = run.rep.Accepted - run.rep.Added

	if run.rep.Accepted == 0 {
		_, fell, err := run.store.Unlink(ctx, run.target.Key) // nothing was written: the live set goes
		if err != nil {
			return fmt.Errorf("remove the live set: %w", err)
		}

		run.rep.FellBackToDel = fell
		run.rep.Swapped = true

		return run.bump(ctx)
	}

	// a temporary key that expired (or was removed) would leave the segment empty after the swap: RENAME of the live
	// key to the old name has run when the second RENAME fails
	exists, _, err := run.store.KeyTTL(ctx, run.tmp)
	if err != nil {
		return fmt.Errorf("check the temporary key: %w", err)
	}

	if !exists {
		return ErrTempGone
	}

	err = run.store.Swap(ctx, run.target.Key, run.tmp, run.old, run.opts.TempTTL)
	if err != nil {
		return fmt.Errorf("swap: %w", err)
	}

	run.rep.Swapped = true

	err = run.bump(ctx)
	if err != nil {
		return err
	}

	_, fell, err := run.store.Unlink(ctx, run.old)
	if err != nil {
		return fmt.Errorf("%w: remove the old set (it expires): %w", ErrSwapped, err)
	}

	run.rep.FellBackToDel = run.rep.FellBackToDel || fell

	return nil
}

// bump changes the version key, after the swap, so that a relay that sees the new version finds the new set.
func (run *importRun) bump(ctx context.Context) error {
	if run.target.VersionKey == "" {
		return nil
	}

	return Bump(ctx, run.store, run.target, run.rep)
}

// Bump writes a new version to the version key of the target, when it has one, and records it in rep.
func Bump(ctx context.Context, store Store, target *Target, rep *Report) error {
	if target.VersionKey == "" {
		return nil
	}

	version := clock.Now().UTC().Format(time.RFC3339Nano)

	err := store.SetValue(ctx, target.VersionKey, version)
	if err != nil {
		return fmt.Errorf("%w: bump the version key (the relay sees the change at its next full read): %w",
			ErrSwapped, err)
	}

	rep.Version = version

	return nil
}

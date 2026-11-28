package segmentadmin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/blackbirdworks/semaphore/modules/core/segment/redissegment"
)

// settleDelay is how long an edit that stopped with a batch in flight waits before it bumps the version.
const settleDelay = 200 * time.Millisecond

// DefaultScanLimit is the most keys that Stat counts of a keys layout segment.
const DefaultScanLimit = 100_000

// EditOptions set how an add or a remove runs.
type EditOptions struct {
	// Wait pauses between the tries of the version bump. Nil means a timer.
	Wait func(ctx context.Context, delay time.Duration) error
	// BatchSize is the ids of one command. Zero means [DefaultBatchSize].
	BatchSize int
}

// Edit adds the ids of src to the segment (or removes them), in batches, then bumps the version key. Add and remove
// are for small edits of the live key: there is no temporary key and no swap. An edit of a set is refused while an
// import holds the lock (the check and the write are one step on the server), because the swap would lose it.
//
// When a failure or the end of the context stops the edit after a batch was sent (a lost reply may hide that it was
// applied), the version key is still bumped, on a context of its own, so that the relays see what may have been
// applied, and the error says how much was confirmed: [ErrPartial].
func Edit(
	ctx context.Context,
	store Store,
	target *Target,
	src Source,
	remove bool,
	opts EditOptions,
) (*Report, error) {
	if opts.BatchSize == 0 {
		opts.BatchSize = DefaultBatchSize
	}

	if opts.BatchSize < 0 || opts.BatchSize > MaxBatchSize {
		return nil, fmt.Errorf("%w: batch size must be 1 to %d", ErrBadOptions, MaxBatchSize)
	}

	run := &importRun{
		store: store, target: target, opts: ImportOptions{BatchSize: opts.BatchSize, Wait: opts.Wait}, rep: &Report{},
	}

	err := run.editAll(ctx, src, remove)
	if err != nil {
		if run.rep.Applied == 0 && !run.maybe {
			return run.rep, err
		}

		// the run stops sending. A batch that was sent and whose answer is not known may still be running on the
		// server: give it a moment (bounded) to finish before the bump that announces it, then bump on a context of
		// its own. When the store is slower than that, the bump can come first: the error says to bump once more.
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()

		if run.maybe {
			wait := opts.Wait
			if wait == nil {
				wait = waitTimer
			}

			_ = wait(cctx, settleDelay)
		}

		bumpErr := BumpWithRetry(cctx, store, target, run.rep, opts.Wait)

		return run.rep, fmt.Errorf(
			"%w: %d of %d ids confirmed (if the store was slow, run `semaphore-cli segment bump` "+
				"once more): %w",
			ErrPartial,
			run.rep.Applied,
			run.rep.Accepted,
			errors.Join(err, bumpErr),
		)
	}

	if run.rep.Applied == 0 {
		return run.rep, nil // nothing changed: the relay has nothing to learn
	}

	// the batches are applied: the bump runs on a context of its own, whatever became of the run's
	bctx, bcancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer bcancel()

	return run.rep, BumpWithRetry(bctx, store, target, run.rep, opts.Wait)
}

// editAll reads the input and applies it in batches.
func (run *importRun) editAll(ctx context.Context, src Source, remove bool) error {
	for {
		err := ctx.Err()
		if err != nil {
			return fmt.Errorf("edit stopped: %w", err)
		}

		rec, err := src.Next()
		if errors.Is(err, io.EOF) {
			return run.apply(ctx, remove)
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
			err = run.apply(ctx, remove)
			if err != nil {
				return err
			}
		}
	}
}

// apply sends the batch to the live key.
func (run *importRun) apply(ctx context.Context, remove bool) error {
	if len(run.batch) == 0 {
		return nil
	}

	size := int64(len(run.batch))

	defer func() { run.batch = run.batch[:0] }()

	var (
		count  int64
		locked bool
		err    error
	)

	run.maybe = true // from here on, a lost reply may hide a batch that was applied

	switch {
	case run.target.Layout == redissegment.LayoutKeys && remove:
		count, _, err = run.store.Unlink(ctx, prefixed(run.target.Key, run.batch)...)
	case run.target.Layout == redissegment.LayoutKeys:
		count, err = run.store.SetKeys(ctx, prefixed(run.target.Key, run.batch))
	default:
		keys, keysErr := ImportKeys(run.target.Key, "")
		if keysErr != nil {
			run.maybe = false

			return keysErr
		}

		count, locked, err = run.store.EditSet(ctx, keys.Lock, run.target.Key, run.batch, remove)
	}

	if err != nil {
		return fmt.Errorf("write a batch: %w", err)
	}

	if locked {
		run.maybe = false // refused: nothing was written

		return ErrImportRunning
	}

	run.rep.Applied += size

	if remove {
		run.rep.Removed += count
	} else {
		run.rep.Added += count
	}

	return nil
}

func prefixed(prefix string, ids []string) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = prefix + id
	}

	return out
}

// Stats is what Stat tells of a segment.
type Stats struct {
	Version    string
	Count      int64
	TTL        time.Duration
	LockTTL    time.Duration
	Exists     bool
	AtLeast    bool
	HasVersion bool
	LockHeld   bool
}

// Stat reads the existence, the cardinality, the version and the ttl of a segment. A keys layout segment is counted by
// SCAN, up to scanLimit keys (zero means [DefaultScanLimit]).
func Stat(ctx context.Context, store Store, target *Target, scanLimit int) (*Stats, error) {
	if scanLimit <= 0 {
		scanLimit = DefaultScanLimit
	}

	out := &Stats{}

	var err error
	if target.Layout == redissegment.LayoutKeys {
		err = statKeys(ctx, store, target, scanLimit, out)
	} else {
		err = statSet(ctx, store, target, out)
	}

	if err != nil {
		return nil, err
	}

	if target.Layout != redissegment.LayoutKeys {
		keys, keysErr := ImportKeys(target.Key, "")
		if keysErr != nil {
			return nil, keysErr
		}

		out.LockHeld, out.LockTTL, err = store.KeyTTL(ctx, keys.Lock)
		if err != nil {
			return nil, fmt.Errorf("read the import lock: %w", err)
		}
	}

	if target.VersionKey != "" {
		out.Version, out.HasVersion, err = store.Value(ctx, target.VersionKey)
		if err != nil {
			return nil, fmt.Errorf("read the version key: %w", err)
		}
	}

	return out, nil
}

func statKeys(ctx context.Context, store Store, target *Target, scanLimit int, out *Stats) error {
	count, more, err := store.CountKeys(ctx, target.Key, scanLimit)
	if err != nil {
		return fmt.Errorf("count the keys: %w", err)
	}

	out.Count, out.AtLeast, out.Exists = int64(count), more, count > 0

	return nil
}

func statSet(ctx context.Context, store Store, target *Target, out *Stats) error {
	exists, ttl, err := store.KeyTTL(ctx, target.Key)
	if err != nil {
		return fmt.Errorf("read the key: %w", err)
	}

	out.Exists, out.TTL = exists, ttl

	if exists {
		out.Count, err = store.SCard(ctx, target.Key)
		if err != nil {
			return fmt.Errorf("read the cardinality: %w", err)
		}
	}

	return nil
}

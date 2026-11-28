package segmentadmin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/blackbirdworks/semaphore/modules/core/segment/redissegment"
)

// DefaultScanLimit is the most keys that Stat counts of a keys layout segment.
const DefaultScanLimit = 1_000_000

// Edit adds the ids of src to the segment (or removes them), in batches of batchSize, then bumps the version key.
// Add and remove are for small edits of the live key: there is no temporary key and no swap.
func Edit(ctx context.Context, store Store, target *Target, src *Reader, remove bool, batchSize int) (*Report, error) {
	if batchSize == 0 {
		batchSize = DefaultBatchSize
	}

	if batchSize < 0 || batchSize > MaxBatchSize {
		return nil, fmt.Errorf("%w: batch size must be 1 to %d", ErrBadOptions, MaxBatchSize)
	}

	run := &importRun{
		store: store, target: target, opts: ImportOptions{BatchSize: batchSize},
		rep: &Report{},
	}

	for {
		err := ctx.Err()
		if err != nil {
			return run.rep, fmt.Errorf("edit stopped: %w", err)
		}

		rec, err := src.Next()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return run.rep, err
		}

		run.rep.Read++

		id, reason := run.accept(&rec)
		if reason != "" {
			run.rep.Rejected.Add(reason)

			continue
		}

		run.rep.Accepted++
		run.batch = append(run.batch, id)

		if len(run.batch) >= batchSize {
			err = run.apply(ctx, remove)
			if err != nil {
				return run.rep, err
			}
		}
	}

	err := run.apply(ctx, remove)
	if err != nil {
		return run.rep, err
	}

	if run.rep.Accepted == 0 {
		return run.rep, nil // nothing changed: the relay has nothing to learn
	}

	return run.rep, Bump(ctx, store, target, run.rep)
}

// apply sends the batch to the live key.
func (run *importRun) apply(ctx context.Context, remove bool) error {
	if len(run.batch) == 0 {
		return nil
	}

	defer func() { run.batch = run.batch[:0] }()

	var (
		count int64
		err   error
	)

	keys := run.target.Layout == redissegment.LayoutKeys

	switch {
	case keys && remove:
		count, _, err = run.store.Unlink(ctx, prefixed(run.target.Key, run.batch)...)
	case keys:
		count, err = run.store.SetKeys(ctx, prefixed(run.target.Key, run.batch))
	case remove:
		count, err = run.store.SRem(ctx, run.target.Key, run.batch)
	default:
		count, err = run.store.SAdd(ctx, run.target.Key, run.batch, 0)
	}

	if err != nil {
		return fmt.Errorf("write a batch: %w", err)
	}

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
	// Version is the value of the version key, when HasVersion.
	Version string
	// Count is the members of the segment (at least that many when AtLeast).
	Count int64
	// TTL is the time to live of the set, zero for none.
	TTL time.Duration
	// Exists tells that the set (or any member key) exists.
	Exists bool
	// AtLeast tells that the count of a keys layout segment hit the bound.
	AtLeast bool
	// HasVersion tells that the segment has a version key and it holds a value.
	HasVersion bool
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

package segmentadmin

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/blackbirdworks/semaphore/modules/core/clock"
)

// DefaultSyncInterval is the pause between two checks of a source.
const DefaultSyncInterval = time.Minute

// maxBackoffFactor bounds the pause after failures to this many intervals.
const maxBackoffFactor = 16

// Outcome is how a round of a sync ended.
type Outcome string

const (
	// OutcomeUnchanged means that the source has the fingerprint of the live set: nothing was written.
	OutcomeUnchanged Outcome = "unchanged"
	// OutcomeChanged means that the source was imported.
	OutcomeChanged Outcome = "changed"
	// OutcomeSkipped means that another import holds the lock: this round did nothing.
	OutcomeSkipped Outcome = "skipped"
	// OutcomeFailed means that the check or the import failed: the live set is what it was.
	OutcomeFailed Outcome = "failed"
)

// SyncOptions set how a sync runs.
type SyncOptions struct {
	Log      *slog.Logger
	Wait     func(ctx context.Context, delay time.Duration) error
	Reader   ReaderOptions
	Import   ImportOptions
	Interval time.Duration
}

// Round is the result of one round.
type Round struct {
	// Err is why the round failed, nil for the other outcomes.
	Err error
	// Report is the import of a changed or failed round, nil when none ran.
	Report *Report
	// Outcome is how the round ended.
	Outcome Outcome
}

// SyncRound checks the source once and imports it when it changed. The fingerprint of the source that is recorded
// next to the live set is compared first, with one read and no write; an import writes the new fingerprint in the step
// of its swap, so a crash can neither skip a change nor leave a fingerprint that does not describe the live set.
func SyncRound(
	ctx context.Context, store Store, target *Target, src *ObjectSource, opts *SyncOptions,
) Round {
	started := clock.Now()
	round := runRound(ctx, store, target, src, opts)

	if opts.Log != nil {
		attrs := []slog.Attr{
			slog.String("outcome", string(round.Outcome)),
			slog.Duration("duration", clock.Now().Sub(started)),
		}

		if rep := round.Report; rep != nil {
			attrs = append(attrs, slog.Int64("read", rep.Read), slog.Int64("accepted", rep.Accepted),
				slog.Int64("added", rep.Added), slog.Bool("swapped", rep.Swapped))
		}

		level := slog.LevelInfo
		if round.Err != nil {
			level = slog.LevelWarn

			attrs = append(attrs, slog.String("error", round.Err.Error()))
		}

		opts.Log.LogAttrs(ctx, level, "segment sync round", attrs...)
	}

	return round
}

func runRound(ctx context.Context, store Store, target *Target, src *ObjectSource, opts *SyncOptions) Round {
	keys, err := ImportKeys(target.Key, "")
	if err != nil {
		return Round{Outcome: OutcomeFailed, Err: err}
	}

	fingerprint, err := src.Fingerprint(ctx)
	if err != nil {
		return Round{Outcome: OutcomeFailed, Err: err}
	}

	stored, _, err := store.Value(ctx, keys.Source)
	if err != nil {
		return Round{Outcome: OutcomeFailed, Err: err}
	}

	if stored == fingerprint {
		return Round{Outcome: OutcomeUnchanged}
	}

	stream, err := src.Open(ctx, fingerprint, opts.Reader)
	if err != nil {
		return Round{Outcome: OutcomeFailed, Err: err}
	}

	defer stream.Close()

	importOpts := opts.Import
	importOpts.Fingerprint = fingerprint

	rep, err := Import(ctx, store, target, stream, importOpts)

	switch {
	case errors.Is(err, ErrImportRunning):
		return Round{Outcome: OutcomeSkipped, Report: rep}
	case err != nil:
		return Round{Outcome: OutcomeFailed, Report: rep, Err: err}
	}

	return Round{Outcome: OutcomeChanged, Report: rep}
}

// Sync runs rounds until ctx ends. A round that fails keeps the live set, and the next one comes after a longer pause
// (the interval doubled for each failure in a row, up to sixteen intervals); any other round is followed by the
// interval. It returns nil when ctx ends: the round in flight has stopped before its swap, or finished it.
func Sync(ctx context.Context, store Store, target *Target, src *ObjectSource, opts *SyncOptions) {
	interval := opts.Interval
	if interval <= 0 {
		interval = DefaultSyncInterval
	}

	wait := opts.Wait
	if wait == nil {
		wait = waitTimer
	}

	failures := 0

	for ctx.Err() == nil {
		round := SyncRound(ctx, store, target, src, opts)

		delay := interval

		if round.Outcome == OutcomeFailed {
			delay = interval << min(failures, maxBackoffFactor)
			delay = min(delay, interval*maxBackoffFactor)
			failures++
		} else {
			failures = 0
		}

		_ = wait(ctx, delay) // it returns when ctx ends, and the loop ends with it
	}
}

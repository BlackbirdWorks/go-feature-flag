package segmentadmin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/blackbirdworks/semaphore/modules/core/clock"
)

// DefaultSyncInterval is the pause between two checks of a source.
const DefaultSyncInterval = time.Minute

// The bounds of the interval of a sync, and of the pause after failures.
const (
	// MinSyncInterval and MaxSyncInterval bound SyncOptions.Interval.
	MinSyncInterval = time.Second
	MaxSyncInterval = 24 * time.Hour
	// maxBackoffShift bounds the pause after failures to 1<<maxBackoffShift intervals, and never above the longest
	// interval.
	maxBackoffShift = 4
)

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
	// OutcomeStopped means that the context ended during the round: the live set is what it was, or the new one.
	OutcomeStopped Outcome = "stopped"
)

// SyncOptions set how a sync runs.
type SyncOptions struct {
	// Log gets one line for each round (counts only, never an id). Nil logs nothing.
	Log *slog.Logger
	// Wait pauses between rounds. Nil means a timer; a test injects one that returns at once.
	Wait func(ctx context.Context, delay time.Duration) error
	// Reader sets how the objects are read.
	Reader ReaderOptions
	// Import sets how the import of a changed source runs. Its Fingerprint is set by the round.
	Import ImportOptions
	// Interval is the pause between two rounds, from [MinSyncInterval] to [MaxSyncInterval]. Zero means
	// [DefaultSyncInterval].
	Interval time.Duration
}

// Validate checks the interval.
func (o *SyncOptions) Validate() error {
	if o.Interval != 0 && (o.Interval < MinSyncInterval || o.Interval > MaxSyncInterval) {
		return fmt.Errorf("%w: the interval must be %s to %s", ErrBadOptions, MinSyncInterval, MaxSyncInterval)
	}

	return nil
}

// Round is the result of one round.
type Round struct {
	// Err is why the round failed or was stopped, nil for the other outcomes.
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

	if round.Outcome == OutcomeFailed && ctx.Err() != nil {
		round.Outcome = OutcomeStopped
	}

	if opts.Log != nil {
		logRound(ctx, opts.Log, &round, clock.Now().Sub(started))
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
// (the interval doubled for each failure in a row, up to sixteen intervals and never above [MaxSyncInterval]); any
// other round is followed by the interval. It returns when ctx ends: the round in flight has stopped before its swap,
// or finished it. The error is a bad option.
func Sync(ctx context.Context, store Store, target *Target, src *ObjectSource, opts *SyncOptions) error {
	err := opts.Validate()
	if err != nil {
		return err
	}

	interval := opts.Interval
	if interval == 0 {
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
			delay = backoff(interval, failures)
			failures++
		} else {
			failures = 0
		}

		_ = wait(ctx, delay) // it returns when ctx ends, and the loop ends with it
	}

	return nil
}

// backoff is the pause after failures in a row: the interval doubled each time, up to 1<<maxBackoffShift intervals.
// The interval is at most [MaxSyncInterval], so the product cannot overflow.
func backoff(interval time.Duration, failures int) time.Duration {
	return min(interval<<min(failures, maxBackoffShift), MaxSyncInterval)
}

// logRound writes the line of a round: counts and the duration, never an id. A skipped round and a failed one are
// warnings with their reason; a round that the end of the context stopped is information.
func logRound(ctx context.Context, log *slog.Logger, round *Round, took time.Duration) {
	attrs := []slog.Attr{slog.String("outcome", string(round.Outcome)), slog.Duration("duration", took)}

	if rep := round.Report; rep != nil {
		attrs = append(attrs, slog.Int64("read", rep.Read), slog.Int64("accepted", rep.Accepted),
			slog.Int64("added", rep.Added), slog.Bool("swapped", rep.Swapped))
	}

	level := slog.LevelInfo

	switch {
	case round.Outcome == OutcomeSkipped:
		level = slog.LevelWarn

		attrs = append(attrs, slog.String("reason",
			"another import holds the lock of the segment (a dead run's lock expires by its ttl: see segment stat)"))
	case round.Outcome == OutcomeStopped:
		attrs = append(attrs, slog.String("reason", "the command is stopping"))
	case round.Err != nil:
		level = slog.LevelWarn

		attrs = append(attrs, slog.String("error", round.Err.Error()))
	}

	log.LogAttrs(ctx, level, "segment sync round", attrs...)
}

package events

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/exporter"
)

// Pipeline is the behaviour of the queue of one exporter: what a full queue does, the limit of a batch in bytes, the
// retry of a failed export and the drain deadline. The zero value is the behaviour before these settings existed:
// a full queue drops the new event, no byte limit, a failed batch stays for the next flush, the drain deadline of the
// dispatcher.
type Pipeline struct {
	// Overflow is one of the bootstrap.Overflow* policies; empty is dropNewest.
	Overflow string
	// Retry is the retry of a failed export; its zero value turns the retry of the pipeline off.
	Retry RetryPolicy
	// BlockTimeout is the longest wait of the block policy for room in the queue.
	BlockTimeout time.Duration
	// DrainTimeout is the deadline of the drain of the exporter; zero is the one of the dispatcher.
	DrainTimeout time.Duration
	// MaxBytes cuts a batch at that size (see [exporter.EventSize]); zero is no limit.
	MaxBytes int64
}

// RetryPolicy is the retry of a failed export: MaxAttempts counts the first attempt (0 is one attempt, like 1: a failed
// batch then follows Drop), the wait before a retry starts at InitialBackoff, doubles up to MaxBackoff and carries
// jitter, and the waits of one batch add up to MaxElapsed at most (0: no cap).
type RetryPolicy struct {
	// Drop makes a batch that exhausts the retry dropped and counted as retry_exhausted; the default keeps it for
	// the next flush of the engine manager.
	Drop           bool
	MaxAttempts    int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	MaxElapsed     time.Duration
}

// PipelineOf reads the pipeline settings of an exporter entry. The bootstrap loader has applied the defaults.
func PipelineOf(exp *bootstrap.Exporter) Pipeline {
	pipe := Pipeline{Overflow: exp.OverflowPolicy}

	if exp.BlockTimeout != nil {
		pipe.BlockTimeout = time.Duration(*exp.BlockTimeout)
	}

	if exp.DrainTimeout != nil {
		pipe.DrainTimeout = time.Duration(*exp.DrainTimeout)
	}

	if exp.MaxBytes != nil {
		pipe.MaxBytes = int64(*exp.MaxBytes)
	}

	if retry := exp.Retry; retry != nil {
		pipe.Retry.Drop = retry.OnExhausted == bootstrap.RetryDrop

		if retry.MaxAttempts != nil {
			pipe.Retry.MaxAttempts = *retry.MaxAttempts
		}

		if retry.InitialBackoff != nil {
			pipe.Retry.InitialBackoff = time.Duration(*retry.InitialBackoff)
		}

		if retry.MaxBackoff != nil {
			pipe.Retry.MaxBackoff = time.Duration(*retry.MaxBackoff)
		}

		if retry.MaxElapsed != nil {
			pipe.Retry.MaxElapsed = time.Duration(*retry.MaxElapsed)
		}
	}

	return pipe
}

// errHalted is returned instead of an attempt that the drain deadline of the exporter forbids: the events stay with
// the engine manager, which is closing, and the instance counts them as drain drops.
var errHalted = errors.New("events: the drain deadline passed, no new attempt")

// halted says whether the drain deadline of the exporter passed.
func (c *countingExporter) halted() bool {
	select {
	case <-c.halt:
		return true
	default:
		return false
	}
}

const (
	// backoffFactor is the growth of the wait of a retry.
	backoffFactor = 2
	// jitterFloor is the share of the backoff that a wait always keeps: the wait is in [floor, 1] of it.
	jitterFloor = 0.5
)

// policy is the overflow policy, with the default.
func (p *Pipeline) policy() string {
	if p.Overflow == "" {
		return bootstrap.OverflowDropNewest
	}

	return p.Overflow
}

// flushStart is the time the budget of the retry of the batch runs from: the first attempt of the flush that the
// batch belongs to, shared by the batches the byte limit cut it into, or now for a call outside a numbered flush.
func (c *countingExporter) flushStart(ctx context.Context) time.Time {
	id := exporter.FlushID(ctx)
	if id == 0 {
		return c.now()
	}

	c.flushMu.Lock()
	defer c.flushMu.Unlock()

	if id != c.flushID {
		c.flushID, c.flushAt = id, c.now()
	}

	return c.flushAt
}

// exportBatch exports one batch, retrying a failure as the policy says: the waits and the time of the attempts of the
// flush add up to MaxElapsed at most. A batch that fails through every attempt is handed back to the engine
// manager, which keeps it for its next flush, or with the policy drop is dropped, counted and reported as exported.
// A retry that the drain deadline ends returns the error: the events stay with the manager, which is closing.
func (c *countingExporter) exportBatch(
	ctx context.Context,
	logger *slog.Logger,
	events []exporter.ExportableEvent,
) error {
	retry := &c.pipe.Retry
	if retry.MaxAttempts <= 0 {
		return c.single(ctx, logger, events)
	}

	start := c.flushStart(ctx)
	backoff := retry.InitialBackoff

	for attempt := 1; ; attempt++ {
		// after the drain deadline no attempt starts; one in flight may finish
		if c.halted() {
			return errHalted
		}

		err := c.attempt(ctx, logger, events)
		if err == nil {
			return nil
		}

		wait := c.jitter(backoff)

		if attempt >= retry.MaxAttempts || retry.MaxElapsed > 0 && c.now().Sub(start)+wait > retry.MaxElapsed {
			return c.exhaust(ctx, logger, len(events), attempt, err)
		}

		select {
		case <-c.after(wait):
		case <-c.halt:
			return err
		case <-ctx.Done():
			return err
		}

		backoff = nextBackoff(backoff, retry.MaxBackoff)
	}
}

// single is one attempt, then the rule of onExhausted: the export of a pipeline without retry.
func (c *countingExporter) single(ctx context.Context, logger *slog.Logger, events []exporter.ExportableEvent) error {
	if c.halted() {
		return errHalted
	}

	err := c.attempt(ctx, logger, events)
	if err != nil && c.pipe.Retry.Drop {
		return c.exhaust(ctx, logger, len(events), 1, err)
	}

	return err
}

// nextBackoff doubles the backoff up to the cap (an hour when there is none), without overflowing.
func nextBackoff(backoff, limit time.Duration) time.Duration {
	if limit <= 0 {
		limit = time.Hour
	}

	if backoff >= limit/backoffFactor {
		return limit
	}

	return backoff * backoffFactor
}

// jitter is the wait before a retry: half of the backoff, plus a random share of the other half.
func (c *countingExporter) jitter(backoff time.Duration) time.Duration {
	return time.Duration(float64(backoff) * (jitterFloor + (1-jitterFloor)*c.rnd()))
}

// exhaust ends the retry of a batch: with the policy drop the batch is dropped and counted, otherwise the error goes
// back to the engine manager, which keeps the events.
func (c *countingExporter) exhaust(ctx context.Context, logger *slog.Logger, count, attempts int, err error) error {
	keep := !c.pipe.Retry.Drop

	if logger != nil {
		attrs := []slog.Attr{
			slog.Int("events", count), slog.Int("attempts", attempts), slog.String("class", string(classify(err))),
			slog.String("error", err.Error()),
		}

		if keep {
			logger.LogAttrs(
				ctx,
				slog.LevelWarn,
				"a batch is kept for the next flush: the export failed through every attempt",
				attrs...)
		} else {
			logger.LogAttrs(
				ctx,
				slog.LevelError,
				"a batch is dropped: the export failed through every attempt",
				attrs...)
		}
	}

	if keep {
		return err
	}

	dropped := uint64(max(count, 0))

	c.instance.exhausted.Add(dropped)
	c.name.exhausted.Add(dropped)

	return nil
}

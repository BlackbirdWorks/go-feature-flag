package events

import (
	"context"
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

// RetryPolicy is the retry of a failed export: MaxAttempts counts the first attempt (0 turns the retry off, 1 drops
// a failed batch at once), the wait before a retry starts at InitialBackoff, doubles up to MaxBackoff and carries
// jitter, and the waits of one batch add up to MaxElapsed at most (0: no cap).
type RetryPolicy struct {
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

// chunks cuts the events into the batches the byte limit allows: a batch is closed when the next event would
// pass the limit, and an event larger than the limit goes alone.
func (c *countingExporter) chunks(events []exporter.ExportableEvent) [][]exporter.ExportableEvent {
	limit := int(c.pipe.MaxBytes)
	if limit <= 0 || len(events) == 0 {
		return [][]exporter.ExportableEvent{events}
	}

	var (
		out   [][]exporter.ExportableEvent
		start int
		size  int
	)

	for i, event := range events {
		each := exporter.EventSize(event)

		if i > start && size+each > limit {
			out = append(out, events[start:i])
			start, size = i, 0
		}

		size += each
	}

	return append(out, events[start:])
}

// exportChunk exports one batch, retrying a failure as the policy says. A batch that fails through every attempt
// is dropped, counted and reported as exported to the engine manager, which would otherwise keep it for ever. A
// retry that the drain deadline ends returns the error: the events stay with the manager, which is closing.
func (c *countingExporter) exportChunk(
	ctx context.Context,
	logger *slog.Logger,
	events []exporter.ExportableEvent,
) error {
	retry := &c.pipe.Retry
	backoff := retry.InitialBackoff

	var waited time.Duration

	for attempt := 1; ; attempt++ {
		err := c.attempt(ctx, logger, events)
		if err == nil || retry.MaxAttempts <= 0 {
			return err
		}

		wait := c.jitter(backoff)

		if attempt >= retry.MaxAttempts || retry.MaxElapsed > 0 && waited+wait > retry.MaxElapsed {
			c.exhaust(ctx, logger, len(events), attempt, err)

			return nil
		}

		select {
		case <-c.after(wait):
		case <-c.haltDone():
			return err
		case <-ctx.Done():
			return err
		}

		waited += wait
		backoff = min(backoff*backoffFactor, retry.MaxBackoff)
	}
}

// jitter is the wait before a retry: half of the backoff, plus a random share of the other half.
func (c *countingExporter) jitter(backoff time.Duration) time.Duration {
	return time.Duration(float64(backoff) * (jitterFloor + (1-jitterFloor)*c.rnd()))
}

// haltDone is the channel of the end of the drain of the exporter; nil (it never fires) while it runs.
func (c *countingExporter) haltDone() <-chan struct{} {
	if halt := c.halt.Load(); halt != nil {
		return (*halt).Done()
	}

	return nil
}

// exhaust drops a batch whose retries are spent.
func (c *countingExporter) exhaust(ctx context.Context, logger *slog.Logger, count, attempts int, err error) {
	dropped := uint64(max(count, 0))

	c.instance.exhausted.Add(dropped)
	c.name.exhausted.Add(dropped)

	if logger != nil {
		logger.LogAttrs(ctx, slog.LevelError, "a batch is dropped: the export failed through every attempt",
			slog.Int("events", count), slog.Int("attempts", attempts), slog.String("class", string(classify(err))),
			slog.String("error", err.Error()))
	}
}

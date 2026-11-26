package events

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
)

// notifierRun is a running notifier: its queue, its goroutine and its counters.
type notifierRun struct {
	logger    *slog.Logger
	queue     chan flagruntime.Change
	finished  chan struct{}
	def       NotifierDef
	last      lastError
	delivered atomic.Uint64
	errors    atomic.Uint64
	panics    atomic.Uint64
	dropped   atomic.Uint64
	state     atomic.Int32
	// abandoned is set when the drain deadline passed: the goroutine drops what is left in the queue.
	abandoned atomic.Bool
}

func newNotifierRun(ctx context.Context, def NotifierDef, cfg *NotificationsConfig) *notifierRun {
	run := &notifierRun{
		def:      def,
		queue:    make(chan flagruntime.Change, cfg.QueueSize),
		finished: make(chan struct{}),
		logger:   cfg.Logger.With(slog.String("notifier", def.Name), slog.String("kind", def.Kind)),
	}

	run.last.set(ErrorNone)

	go run.loop(context.WithoutCancel(ctx))

	return run
}

// offer queues the change, or drops it when the queue is full. It never blocks.
func (r *notifierRun) offer(change flagruntime.Change) {
	select {
	case r.queue <- change:
	default:
		r.dropped.Add(1)
	}
}

// loop calls the notifier for each queued change, until the queue is closed and empty.
func (r *notifierRun) loop(ctx context.Context) {
	defer close(r.finished)

	for change := range r.queue {
		if r.abandoned.Load() {
			r.dropped.Add(1)

			continue
		}

		r.call(ctx, change)
	}
}

// call runs the notifier for one change: an error is counted and logged, a panic is recovered.
func (r *notifierRun) call(ctx context.Context, change flagruntime.Change) {
	defer func() {
		if rec := recover(); rec != nil {
			r.panics.Add(1)
			r.last.set(ErrorPanic)
			r.logger.LogAttrs(ctx, slog.LevelError, "the notifier panicked",
				slog.String("flagSet", change.FlagSet), slog.String("panic", fmt.Sprint(rec)))
		}
	}()

	err := r.def.Notifier.Notify(change.Diff)
	if err != nil {
		r.errors.Add(1)
		r.last.set(classify(err))
		// the message of the error is not logged: a notifier error can carry the webhook URL.
		r.logger.LogAttrs(ctx, slog.LevelError, "the notifier failed",
			slog.String("flagSet", change.FlagSet), slog.String("class", string(classify(err))))

		return
	}

	r.delivered.Add(1)
	r.last.set(ErrorNone)
}

// close stops taking changes; the goroutine delivers the queue and ends. The caller guarantees that nobody
// offers any more.
func (r *notifierRun) close() {
	r.state.Store(stateDraining)
	close(r.queue)
}

// wait waits for the goroutine until the deadline of ctx. On the deadline the goroutine drops (and counts) the
// changes still queued; a call in progress cannot be cancelled and ends by itself.
func (r *notifierRun) wait(ctx context.Context) bool {
	select {
	case <-r.finished:
		r.state.Store(stateStopped)

		return true
	case <-ctx.Done():
		r.abandoned.Store(true)
		r.state.Store(stateCut)

		return false
	}
}

func (r *notifierRun) status() NotifierStatus {
	return NotifierStatus{
		Name:      r.def.Name,
		Kind:      r.def.Kind,
		State:     stateName(r.state.Load()),
		LastError: r.last.get(),
		Queued:    len(r.queue),
		Delivered: r.delivered.Load(),
		Errors:    r.errors.Load(),
		Panics:    r.panics.Load(),
		Dropped:   r.dropped.Load(),
	}
}

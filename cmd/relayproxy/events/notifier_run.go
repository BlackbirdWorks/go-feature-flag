package events

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
)

// notifierName is the notifier name: the counters that survive the reloads, and its latest instance.
type notifierName struct {
	latest    atomic.Pointer[notifierRun]
	name      string
	delivered atomic.Uint64
	errors    atomic.Uint64
	panics    atomic.Uint64
	dropped   atomic.Uint64
	last      lastError
}

func (e *notifierName) status() NotifierStatus {
	out := NotifierStatus{
		Name:      e.name,
		LastError: e.last.get(),
		Delivered: e.delivered.Load(),
		Errors:    e.errors.Load(),
		Panics:    e.panics.Load(),
		Dropped:   e.dropped.Load(),
	}

	if latest := e.latest.Load(); latest != nil {
		out.Kind = latest.def.Kind
		out.State = stateName(latest.state.Load())
		out.Queued = len(latest.queue)
	}

	return out
}

// notifierRun is a running notifier instance: its queue and its goroutine.
type notifierRun struct {
	logger   *slog.Logger
	queue    chan flagruntime.Change
	finished chan struct{}
	// abandon is closed when the drain deadline passed: the goroutine drops what is left in the queue, and the
	// instance that follows it stops waiting for it.
	abandon chan struct{}
	// prev is the instance this one replaces: this one calls its notifier after prev delivered its queue.
	prev        *notifierRun
	name        *notifierName
	def         NotifierDef
	state       atomic.Int32
	closeOnce   sync.Once
	abandonOnce sync.Once
}

func newNotifierRun(
	ctx context.Context, def NotifierDef, name *notifierName, prev *notifierRun, cfg *NotificationsConfig,
) *notifierRun {
	run := &notifierRun{
		def:      def,
		name:     name,
		prev:     prev,
		queue:    make(chan flagruntime.Change, cfg.QueueSize),
		finished: make(chan struct{}),
		abandon:  make(chan struct{}),
		logger:   cfg.Logger.With(slog.String("notifier", def.Name), slog.String("kind", def.Kind)),
	}

	go run.loop(context.WithoutCancel(ctx))

	return run
}

// offer queues the change, or drops it when the queue is full. It never blocks.
func (r *notifierRun) offer(change flagruntime.Change) {
	select {
	case r.queue <- change:
	default:
		r.name.dropped.Add(1)
	}
}

// loop calls the notifier for each queued change, until the queue is closed and empty. It starts after the
// instance it replaces, so that the changes of a flag set reach the notifier in order across a reload.
func (r *notifierRun) loop(ctx context.Context) {
	defer close(r.finished)
	// a run that was cut and finishes later is stopped, whoever waited for it
	defer r.state.Store(stateStopped)

	if r.prev != nil {
		select {
		case <-r.prev.finished:
		case <-r.prev.abandon:
		}
	}

	r.prev = nil // the chain of replaced instances does not stay in memory

	for change := range r.queue {
		select {
		case <-r.abandon:
			r.name.dropped.Add(1)

			continue
		default:
		}

		r.call(ctx, change)
	}
}

// call runs the notifier for one change: an error is counted and logged, a panic is recovered.
func (r *notifierRun) call(ctx context.Context, change flagruntime.Change) {
	defer func() {
		if rec := recover(); rec != nil {
			r.name.panics.Add(1)
			r.name.last.set(ErrorPanic)
			// the value of a panic can carry a secret: only its type is logged
			r.logger.LogAttrs(ctx, slog.LevelError, "the notifier panicked",
				slog.String("flagSet", change.FlagSet), slog.String("type", fmt.Sprintf("%T", rec)))
		}
	}()

	err := r.def.Notifier.Unseal().Notify(change.Diff)
	if err != nil {
		r.name.errors.Add(1)
		r.name.last.set(classify(err))
		// the message of the error is not logged: a notifier error can carry the webhook URL.
		r.logger.LogAttrs(ctx, slog.LevelError, "the notifier failed",
			slog.String("flagSet", change.FlagSet), slog.String("class", string(classify(err))))

		return
	}

	r.name.delivered.Add(1)
	r.name.last.set(ErrorNone)
}

// close stops taking changes; the goroutine delivers the queue and ends. The caller guarantees that nobody
// offers any more.
func (r *notifierRun) close() {
	r.closeOnce.Do(func() {
		r.state.CompareAndSwap(stateRunning, stateDraining)
		close(r.queue)
	})
}

// isFinished says whether the goroutine of the instance ended.
func (r *notifierRun) isFinished() bool {
	select {
	case <-r.finished:
		return true
	default:
		return false
	}
}

// wait waits for the goroutine until the deadline of ctx. On the deadline the goroutine drops (and counts) the
// changes still queued; a call in progress cannot be cancelled and ends by itself.
func (r *notifierRun) wait(ctx context.Context) bool {
	// a run that finished is not cut, whatever the deadline: with both ready, select would pick at random
	if r.isFinished() {
		r.state.Store(stateStopped)

		return true
	}

	select {
	case <-r.finished:
		r.state.Store(stateStopped)

		return true
	case <-ctx.Done():
		r.abandonOnce.Do(func() { close(r.abandon) })
		r.state.Store(stateCut)

		return false
	}
}

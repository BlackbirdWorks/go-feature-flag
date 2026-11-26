package events

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
)

// RuntimeConfig configures a [Runtime]. Zero values take the defaults of the dispatcher and the notifications.
type RuntimeConfig struct {
	// Logger receives the logs; nil discards them.
	Logger *slog.Logger
	// Broadcast is the hook called with every change, see [NotificationsConfig.Broadcast].
	Broadcast func(flagruntime.Change)
	// NewManager builds the manager of one exporter, see [DispatcherConfig.NewManager].
	NewManager NewManagerFunc
	// Builder builds the exporters and notifiers.
	Builder Builder
	// QueueSize, Workers and BatchSize size the event channel, the worker pool and its batches.
	QueueSize int
	Workers   int
	BatchSize int
	// NotifierQueueSize is the queue of changes of each notifier.
	NotifierQueueSize int
	// DrainTimeout is the deadline of the drain of what a reload replaces or removes.
	DrainTimeout time.Duration
}

// Runtime is the dispatcher and the notification worker under one lifecycle.
type Runtime struct {
	Dispatcher    *Dispatcher
	Notifications *Notifications
	builder       Builder
}

// NewRuntime returns a runtime with no exporter and no notifier.
func NewRuntime(cfg *RuntimeConfig) *Runtime {
	return &Runtime{
		builder: cfg.Builder,
		Dispatcher: NewDispatcher(&DispatcherConfig{
			Logger: cfg.Logger, NewManager: cfg.NewManager, Builder: cfg.Builder, QueueSize: cfg.QueueSize,
			Workers: cfg.Workers, BatchSize: cfg.BatchSize, DrainTimeout: cfg.DrainTimeout,
		}),
		Notifications: NewNotifications(&NotificationsConfig{
			Logger: cfg.Logger, Broadcast: cfg.Broadcast, Builder: cfg.Builder, QueueSize: cfg.NotifierQueueSize,
			DrainTimeout: cfg.DrainTimeout,
		}),
	}
}

// Start starts the dispatcher and the consumer of the changes channel (nil for none). The end of ctx shuts both
// down, each with the drain timeout of the configuration.
func (r *Runtime) Start(ctx context.Context, changes <-chan flagruntime.Change) error {
	err := r.Dispatcher.Start(ctx)
	if err != nil {
		return err
	}

	return r.Notifications.Start(ctx, changes)
}

// Collect hands an evaluation event to the exporters, see [Dispatcher.Collect].
func (r *Runtime) Collect(event Event) bool { return r.Dispatcher.Collect(event) }

// ReloadReport says what a reload did with the exporters and the notifiers.
type ReloadReport struct {
	Exporters ReloadSet
	Notifiers ReloadSet
}

// Reload builds the exporters and the notifiers of the specs, checks that both sides are open, and applies both
// in place. When an entry fails to build, or one side is closed, nothing is changed and the error says why (every
// failing entry is named).
func (r *Runtime) Reload(ctx context.Context, specs Specs) (ReloadReport, error) {
	expDefs, expErr := r.builder.Exporters(ctx, specs)
	notDefs, notErr := r.builder.Notifiers(ctx, specs)

	err := errors.Join(expErr, notErr)
	if err != nil {
		return ReloadReport{}, err
	}

	if r.Dispatcher.closed.Load() {
		return ReloadReport{}, ErrClosed
	}

	err = r.Notifications.open()
	if err != nil {
		return ReloadReport{}, err
	}

	var report ReloadReport

	report.Exporters, err = r.Dispatcher.Apply(ctx, expDefs)
	if err != nil {
		return ReloadReport{}, err
	}

	report.Notifiers, err = r.Notifications.Apply(ctx, notDefs)

	return report, err
}

// Shutdown shuts down the notification worker and the dispatcher within the deadline of ctx.
func (r *Runtime) Shutdown(ctx context.Context) (ShutdownReport, error) {
	err := r.Notifications.Shutdown(ctx)

	report, dispErr := r.Dispatcher.Shutdown(ctx)

	return report, errors.Join(err, dispErr)
}

// Status returns the snapshot of the dispatcher and the notifications.
func (r *Runtime) Status() Status {
	st := r.Dispatcher.Status()
	st.Notifiers = r.Notifications.Status().Notifiers

	return st
}

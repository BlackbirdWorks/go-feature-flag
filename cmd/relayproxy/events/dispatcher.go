package events

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/internal"
)

// Defaults of the dispatcher.
const (
	// DefaultQueueSize is the capacity of the event channel.
	DefaultQueueSize = 65536
	// DefaultWorkers is the number of workers that move the events from the channel to the exporters.
	DefaultWorkers = 2
	// DefaultBatchSize is the largest batch a worker hands to the exporters at once.
	DefaultBatchSize = 256
	// DefaultDrainTimeout is the deadline of the drain of a replaced or removed exporter.
	DefaultDrainTimeout = 10 * time.Second
)

// Event is what the evaluation path hands to the dispatcher.
type Event = *exporter.FeatureEvent

// FeatureManager is the engine manager of feature events.
type FeatureManager = exporter.Manager[*exporter.FeatureEvent]

// NewManagerFunc builds the manager of the exporter name. The default is [exporter.NewManager] with that single
// exporter, so M exporters are M managers whatever the number of flag sets.
type NewManagerFunc func(name string, cfg exporter.Config, logger *slog.Logger) FeatureManager

// DispatcherConfig configures a [Dispatcher]. Zero values take the defaults.
type DispatcherConfig struct {
	// Logger receives the logs of the dispatcher; nil discards them.
	Logger *slog.Logger
	// NewManager builds the manager of one exporter; nil uses [exporter.NewManager].
	NewManager NewManagerFunc
	// Builder builds the exporters of [Dispatcher.Reload].
	Builder Builder
	// QueueSize is the capacity of the event channel.
	QueueSize int
	// Workers is the size of the fixed worker pool.
	Workers int
	// BatchSize is the largest batch a worker hands over at once.
	BatchSize int
	// DrainTimeout is the deadline of the drain of an exporter that a reload replaces or removes.
	DrainTimeout time.Duration
}

func (c *DispatcherConfig) defaults() {
	c.Logger = internal.LoggerOrDiscard(c.Logger)

	if c.NewManager == nil {
		c.NewManager = func(_ string, cfg exporter.Config, logger *slog.Logger) FeatureManager {
			return exporter.NewManager[*exporter.FeatureEvent]([]exporter.Config{cfg}, 0, logger)
		}
	}

	if c.QueueSize <= 0 {
		c.QueueSize = DefaultQueueSize
	}

	if c.Workers <= 0 {
		c.Workers = DefaultWorkers
	}

	if c.BatchSize <= 0 {
		c.BatchSize = DefaultBatchSize
	}

	if c.DrainTimeout <= 0 {
		c.DrainTimeout = DefaultDrainTimeout
	}
}

// Dispatcher owns the one bounded event channel and the fixed pool of workers that hands every event to every
// exporter manager. It is safe for concurrent use.
//
// The workers read a batch from the channel and deliver it to the managers of the current set under a read
// lock; a reload swaps the set under the write lock, so a batch goes entirely to the old set or entirely to the
// new one, and the managers that left the set are not touched by any worker when they are drained.
type Dispatcher struct {
	// queue is the one bounded event channel.
	queue chan Event
	state atomic.Pointer[dispatchState]
	done  chan struct{}
	stop  chan struct{}
	// started is closed by Start.
	started chan struct{}
	// unwatch removes the shutdown that the end of the context of Start would run.
	unwatch func() bool
	cfg     DispatcherConfig
	report  ShutdownReport
	workers sync.WaitGroup

	accepted     atomic.Uint64
	dropFull     atomic.Uint64
	dropShutdown atomic.Uint64
	dropDrain    atomic.Uint64
	// inflight counts the collectors between their check of closed and the end of their send.
	inflight atomic.Int64
	// mu guards the delivery of a batch (read) against the swap of the set (write).
	mu sync.RWMutex
	// lifecycle serialises Start, Apply and Shutdown.
	lifecycle sync.Mutex
	closed    atomic.Bool
	aborted   atomic.Bool
	// active is the number of exporters in the set: with none, Collect returns at once.
	active atomic.Int32
	isDone bool
}

// dispatchState is an immutable set of exporters. The retired ones left the set: they drain or are closed.
type dispatchState struct {
	active  []*managed
	retired []*managed
}

// NewDispatcher returns a dispatcher with no exporter. It takes events as soon as it has one, and delivers them
// once [Dispatcher.Start] ran.
func NewDispatcher(config *DispatcherConfig) *Dispatcher {
	cfg := *config
	cfg.defaults()

	disp := &Dispatcher{
		cfg:     cfg,
		queue:   make(chan Event, cfg.QueueSize),
		stop:    make(chan struct{}),
		started: make(chan struct{}),
		done:    make(chan struct{}),
	}
	disp.state.Store(&dispatchState{})

	return disp
}

// Collect hands the event to the exporters. It never blocks and does not allocate. It returns true when the
// event was queued, and false when it was not: there is no exporter (nothing is counted), the channel is full
// or the dispatcher is shut down (both are counted as drops).
func (d *Dispatcher) Collect(event Event) bool {
	if d.active.Load() == 0 {
		return false
	}

	d.inflight.Add(1)

	if d.closed.Load() {
		d.inflight.Add(-1)
		d.dropShutdown.Add(1)

		return false
	}

	select {
	case d.queue <- event:
		d.inflight.Add(-1)
		d.accepted.Add(1)

		return true
	default:
		d.inflight.Add(-1)
		d.dropFull.Add(1)

		return false
	}
}

// Start starts the workers. The context stops the dispatcher like [Dispatcher.Shutdown] with the drain
// timeout of the configuration.
func (d *Dispatcher) Start(ctx context.Context) error {
	d.lifecycle.Lock()
	defer d.lifecycle.Unlock()

	if d.closed.Load() {
		return ErrClosed
	}

	select {
	case <-d.started:
		return ErrAlreadyStarted
	default:
	}

	close(d.started)

	d.workers.Add(d.cfg.Workers)

	for range d.cfg.Workers {
		go d.worker()
	}

	// the end of the context shuts the dispatcher down: no goroutine waits for it, the function runs once
	d.unwatch = context.AfterFunc(ctx, func() {
		drain, cancel := context.WithTimeout(context.WithoutCancel(ctx), d.cfg.DrainTimeout)
		defer cancel()

		_, _ = d.Shutdown(drain)
	})

	return nil
}

// Status returns a snapshot of the queue and of the exporters, without blocking.
func (d *Dispatcher) Status() Status {
	snap := d.state.Load()
	out := Status{
		Queue: QueueStatus{
			Capacity: cap(d.queue),
			Length:   d.queueLen(),
			Accepted: d.accepted.Load(),
			Dropped: Drops{
				QueueFull: d.dropFull.Load(), Shutdown: d.dropShutdown.Load(), Drain: d.dropDrain.Load(),
			},
		},
		Exporters: make([]ExporterStatus, 0, len(snap.active)+len(snap.retired)),
	}

	for _, m := range snap.active {
		out.Exporters = append(out.Exporters, m.status())
	}

	for _, m := range snap.retired {
		out.Exporters = append(out.Exporters, m.status())
	}

	return out
}

func (d *Dispatcher) worker() {
	defer d.workers.Done()

	batch := make([]Event, 0, d.cfg.BatchSize)

	for {
		select {
		case event := <-d.queue:
			batch = append(batch[:0], event)
		case <-d.stop:
			for {
				batch = d.fill(batch[:0])
				if len(batch) == 0 {
					return
				}

				d.deliver(batch)
			}
		}

		d.deliver(d.fill(batch))
	}
}

// fill appends the events that are already waiting, up to the batch size.
func (d *Dispatcher) fill(batch []Event) []Event {
	for len(batch) < d.cfg.BatchSize {
		select {
		case event := <-d.queue:
			batch = append(batch, event)
		default:
			return batch
		}
	}

	return batch
}

// deliver hands the batch to every exporter of the current set.
func (d *Dispatcher) deliver(batch []Event) {
	if d.aborted.Load() {
		d.dropDrain.Add(uint64(len(batch)))

		return
	}

	d.mu.RLock()
	defer d.mu.RUnlock()

	for _, m := range d.state.Load().active {
		m.deliver(batch)
	}
}

// queueLen is the number of events in the channel.
func (d *Dispatcher) queueLen() int { return len(d.queue) }

package events

import (
	"context"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/internal"
)

// Defaults of the dispatcher.
const (
	// DefaultQueueSize is the capacity of the shared event channel.
	DefaultQueueSize = 65536
	// DefaultWorkers is the number of workers that move the events from the channel to the exporters.
	DefaultWorkers = 2
	// DefaultBatchSize is the largest batch a worker or an exporter hands over at once.
	DefaultBatchSize = 256
	// DefaultHandoffSize is the capacity of the hand-off of each exporter, in events.
	DefaultHandoffSize = 16384
	// DefaultDrainTimeout is the deadline of the drain of a replaced or removed exporter.
	DefaultDrainTimeout = 10 * time.Second
)

// Event is what the evaluation path hands to the dispatcher. The event belongs to the dispatcher from the call
// of [Dispatcher.Collect]: the caller must not change it afterwards, the exporters read it concurrently.
type Event = *exporter.FeatureEvent

// FeatureManager is the engine manager of feature events.
type FeatureManager = exporter.Manager[*exporter.FeatureEvent]

// NewManagerFunc builds the manager of the exporter name. The default is [exporter.NewManager] with that single
// exporter, so M exporters are M managers whatever the number of flag sets.
type NewManagerFunc func(name string, cfg exporter.Config, logger *slog.Logger) FeatureManager

// DispatcherConfig configures a [Dispatcher]. Zero values take the defaults.
type DispatcherConfig struct {
	Logger       *slog.Logger
	NewManager   NewManagerFunc
	drainContext func(parent context.Context) (context.Context, context.CancelFunc)
	Builder      Builder
	QueueSize    int
	Workers      int
	BatchSize    int
	HandoffSize  int
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

	if c.HandoffSize <= 0 {
		c.HandoffSize = DefaultHandoffSize
	}

	if c.DrainTimeout <= 0 {
		c.DrainTimeout = DefaultDrainTimeout
	}
}

// Dispatcher owns the one bounded event channel, the fixed pool of workers that hands every event to every
// exporter, and the exporters. It is safe for concurrent use.
//
// The goroutines of a dispatcher are its workers and one goroutine per exporter: their number does not depend
// on the number of flag sets. The workers read a batch from the channel and offer it, without blocking, to the
// hand-off of every exporter of the current set under a read lock; a reload swaps the set under the write lock,
// so a batch goes entirely to the old set or entirely to the new one.
type Dispatcher struct {
	// queue is the one bounded event channel.
	queue chan Event
	state atomic.Pointer[dispatchState]
	// names is the table of the exporter names ever used; its entries keep the counters across reloads. It is
	// replaced (copy on write) under lifecycle, read without a lock.
	names atomic.Pointer[map[string]*nameEntry]
	done  chan struct{}
	stop  chan struct{}
	// started is closed by Start.
	started chan struct{}
	// unwatch removes the shutdown that the end of the context of Start would run.
	unwatch func() bool
	cfg     DispatcherConfig
	report  ShutdownReport
	workers sync.WaitGroup
	// background counts the closing goroutines of the instances.
	background sync.WaitGroup

	accepted       atomic.Uint64
	dropFull       atomic.Uint64
	dropShutdown   atomic.Uint64
	dropNoExporter atomic.Uint64
	dropDrain      atomic.Uint64
	// inflight counts the collectors between their check of closed and the end of their send.
	inflight atomic.Int64
	// mu guards the delivery of a batch (read) against the swap of the set (write).
	mu sync.RWMutex
	// lifecycle serialises Start, the swap of Apply and Shutdown. It is never held while waiting for a drain.
	lifecycle sync.Mutex
	closed    atomic.Bool
	aborted   atomic.Bool
	// active is the number of exporters in the set: with none, Collect returns at once.
	active atomic.Int32
	isDone bool
}

// dispatchState is an immutable set of exporters. The retired ones left the set and are closing or closed.
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
	disp.names.Store(&map[string]*nameEntry{})

	return disp
}

// Collect hands the event to the exporters. It never blocks, takes no lock and does not allocate. It returns
// true when the event was queued, and false when it was not: there is no exporter (nothing is counted), the
// channel is full or the dispatcher is shut down (both are counted as drops). The event must not be changed
// after the call.
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

// Start starts the workers. The end of the context shuts the dispatcher down like [Dispatcher.Shutdown] with the
// drain timeout of the configuration as its deadline.
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

	// no goroutine waits for the end of the context: the function runs once, when it ends
	d.unwatch = context.AfterFunc(ctx, func() {
		drain, cancel := context.WithTimeout(context.WithoutCancel(ctx), d.cfg.DrainTimeout)
		defer cancel()

		_, _ = d.Shutdown(drain)
	})

	return nil
}

// WaitDrains waits until every exporter that left the set has finished closing, including the ones whose drain
// was cut. It returns the error of ctx when that comes first.
func (d *Dispatcher) WaitDrains(ctx context.Context) error {
	finished := make(chan struct{})

	go func() {
		d.background.Wait()
		close(finished)
	}()

	select {
	case <-finished:
		return nil
	case <-ctx.Done():
		return ctx.Err() //nolint:wrapcheck // the context error says it
	}
}

// Status returns a snapshot of the queue and of the exporters, without blocking on anything slower than a
// counter. The exporters are listed by name, with their cumulative counters.
func (d *Dispatcher) Status() Status {
	snap := d.state.Load()
	names := *d.names.Load()
	out := Status{
		Queue: QueueStatus{
			Capacity: cap(d.queue),
			Length:   len(d.queue),
			Accepted: d.accepted.Load(),
			Dropped: Drops{
				QueueFull: d.dropFull.Load(), Shutdown: d.dropShutdown.Load(),
				NoExporter: d.dropNoExporter.Load(), Drain: d.dropDrain.Load(),
			},
		},
		Exporters: make([]ExporterStatus, 0, len(names)),
	}

	all := slices.Concat(snap.active, snap.retired)

	for _, name := range slices.Sorted(maps.Keys(names)) {
		out.Exporters = append(out.Exporters, names[name].status(all))
	}

	return out
}

// status returns the cumulative state of the name; running are the instances that may still hold events.
func (e *nameEntry) status(running []*managed) ExporterStatus {
	stats := &e.stats

	stats.mu.Lock()
	overflow := stats.mgrDropped + stats.handoff.Load()

	for _, inst := range running {
		if inst.name == e && !inst.finalized.Load() {
			overflow += inst.managerDropped()
		}
	}

	handed, exported, drain := stats.handed.Load(), stats.exported.Load(), stats.drain.Load()
	stats.mu.Unlock()

	status := ExporterStatus{
		Name:          e.name,
		LastError:     stats.last.get(),
		Dropped:       Drops{Overflow: overflow, Drain: drain},
		Handed:        handed,
		Exported:      exported,
		FlushFailures: stats.failures.Load(),
		Flushes:       stats.flushes.Load(),
		FlushSeconds:  float64(stats.flushNanos.Load()) / 1e9, //nolint:mnd // nanoseconds per second
	}

	if gone := exported + overflow + drain; handed > gone {
		status.Queued = int64(handed - gone) //nolint:gosec // event counts do not reach 2^63
	}

	if latest := e.latest.Load(); latest != nil {
		status.Kind = latest.def.Kind
		status.State = stateName(latest.state.Load())
	}

	return status
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

// deliver offers the batch to every exporter of the current set, without waiting for any of them. A batch taken
// while the set is empty (a reload to no exporter) is counted as dropped, with the reason no_exporter.
func (d *Dispatcher) deliver(batch []Event) {
	if d.aborted.Load() {
		d.dropDrain.Add(uint64(len(batch)))

		return
	}

	d.mu.RLock()
	defer d.mu.RUnlock()

	active := d.state.Load().active
	if len(active) == 0 {
		d.dropNoExporter.Add(uint64(len(batch)))

		return
	}

	for _, inst := range active {
		inst.offer(batch)
	}
}

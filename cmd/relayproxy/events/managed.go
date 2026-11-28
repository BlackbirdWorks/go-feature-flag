package events

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/exporter"
)

// managed is a running instance of an exporter: its definition, its engine manager, its own bounded hand-off
// and the goroutine that moves the events from the hand-off to the manager.
//
// A worker of the dispatcher never waits on a manager: it offers the events to the hand-off of each instance
// without blocking, and an instance whose hand-off is full drops (and counts) its own events. The manager may
// wait (the engine manager waits for room in its store) in the goroutine of its instance only.
type managed struct {
	mgr FeatureManager
	// finished is closed when the instance finished closing.
	finished chan struct{}
	name     *nameEntry
	// after is the timer of the block deadline and now the clock that stamps it, injected.
	after func(time.Duration) <-chan time.Time
	now   func() time.Time
	// retired is closed when the instance leaves the set: the goroutine that waits for room stops waiting. retire
	// closes it.
	retired <-chan struct{}
	retire  context.CancelFunc
	// stopRetries ends the retries of the exporter (its halt channel); the drain deadline calls it.
	stopRetries context.CancelFunc
	// parked is the waiting room of the block policy: the events that found no room in the hand-off wait there,
	// in the goroutine of the instance, so that no worker waits for a slow exporter. queued counts the batches
	// in it (and the one being placed), blockers is the goroutine.
	parked   chan parkedBatch
	instance *instanceStats
	logger   *slog.Logger
	// in is the hand-off: bounded, closed when the instance leaves the set.
	in       chan Event
	queue    func([]Event)
	consumed chan struct{}
	def      ExporterDef
	pipe     Pipeline
	blockers sync.WaitGroup
	queued   atomic.Int64
	// mgrFinal is the number of events the manager dropped, once it stopped.
	mgrFinal atomic.Uint64
	// undelivered is the number of events the instance could not deliver, known once it finished.
	undelivered atomic.Uint64
	// drain is the deadline of the drain of the instance.
	drain     time.Duration
	batch     int
	closeOnce sync.Once
	state     atomic.Int32
	// finalized says that the counters of the instance were folded in those of its name.
	finalized atomic.Bool
}

// nameEntry is the exporter name: the counters that survive the reloads, and its latest instance.
type nameEntry struct {
	latest atomic.Pointer[managed]
	name   string
	stats  nameStats
}

const (
	stateRunning int32 = iota
	stateDraining
	stateStopped
	stateCut
)

func stateName(state int32) string {
	switch state {
	case stateRunning:
		return StateRunning
	case stateDraining:
		return StateDraining
	case stateStopped:
		return StateStopped
	default:
		return StateCut
	}
}

// newManaged returns an instance and starts the goroutine of its hand-off.
func newManaged(
	def *ExporterDef, mgr FeatureManager, entry *nameEntry, handoff, batch int, logger *slog.Logger,
) *managed {
	return newManagedWith(def, mgr, entry, limits{
		after: time.After, now: time.Now, handoff: handoff, batch: batch, drain: DefaultDrainTimeout,
	}, logger)
}

// newManagedWith is [newManaged] with the clock and the drain deadline of a dispatcher.
func newManagedWith(
	def *ExporterDef, mgr FeatureManager, entry *nameEntry, sizes limits, logger *slog.Logger,
) *managed {
	life, retire := context.WithCancel(context.Background())

	if sizes.now == nil {
		sizes.now = time.Now
	}

	inst := &managed{
		retired:     life.Done(),
		retire:      retire,
		stopRetries: sizes.stopRetries,
		after:       sizes.after,
		now:         sizes.now,
		pipe:        def.Pipeline,
		drain:       cmpOr(def.Pipeline.DrainTimeout, sizes.drain),
		def:         *def,
		mgr:         mgr,
		name:        entry,
		instance:    def.wrapper().instance,
		in:          make(chan Event, sizes.handoff),
		consumed:    make(chan struct{}),
		finished:    make(chan struct{}),
		batch:       sizes.batch,
		logger:      logger,
	}

	if async, ok := mgr.(exporter.AsyncManager[*exporter.FeatureEvent]); ok {
		inst.queue = async.QueueEvents
	} else {
		inst.queue = func(events []Event) {
			for _, event := range events {
				mgr.AddEvent(event)
			}
		}
	}

	if inst.stopRetries == nil {
		inst.stopRetries = func() {}
	}

	go inst.run()

	if def.Pipeline.policy() == bootstrap.OverflowBlock {
		inst.parked = make(chan parkedBatch, parkedBatches)

		inst.blockers.Go(inst.park)
	}

	return inst
}

// parkedBatches is the number of batches that wait for room in the hand-off of an exporter with the block policy.
const parkedBatches = 64

// run moves the events of the hand-off to the manager, in batches, until the hand-off is closed and empty.
func (m *managed) run() {
	defer close(m.consumed)

	batch := make([]Event, 0, m.batch)

	for event := range m.in {
		batch = append(batch[:0], event)
		batch = m.fill(batch)
		m.queue(batch)
	}
}

// fill appends the events that are already waiting, up to the batch size.
func (m *managed) fill(batch []Event) []Event {
	for len(batch) < m.batch {
		select {
		case event, open := <-m.in:
			if !open {
				return batch
			}

			batch = append(batch, event)
		default:
			return batch
		}
	}

	return batch
}

// limits are the sizes and the clock of the dispatcher that an instance takes.
type limits struct {
	after func(time.Duration) <-chan time.Time
	now   func() time.Time
	// stopRetries ends the retries of the exporter; it may be nil.
	stopRetries    context.CancelFunc
	handoff, batch int
	drain          time.Duration
}

// defaultMaxFlushBackoff is the longest pause between the flushes of a failing exporter when the retry has no
// maximum backoff.
const defaultMaxFlushBackoff = 30 * time.Second

// cmpOr returns value, or def when value is zero.
func cmpOr(value, def time.Duration) time.Duration {
	if value != 0 {
		return value
	}

	return def
}

// dropOverflow counts an event lost because the hand-off was full.
func (m *managed) dropOverflow() {
	m.instance.handoff.Add(1)
	m.name.stats.handoff.Add(1)
}

// offer hands the events to the instance without blocking, as the overflow policy says: dropNewest drops what does
// not fit, dropOldest makes room by dropping the oldest queued event, block parks what does not fit in the waiting
// room of the instance (and drops it, counted as a block timeout, when the waiting room is full too). Every drop is
// counted against the instance. The caller is a worker of the dispatcher: it never waits.
func (m *managed) offer(events []Event) {
	// the block deadline of a batch that is parked runs from here: stamped before the batch counts as handed
	var parkedAt time.Time
	if m.pipe.policy() == bootstrap.OverflowBlock {
		parkedAt = m.now()
	}

	count := uint64(len(events))
	m.instance.handed.Add(count)
	m.name.stats.handed.Add(count)

	policy := m.pipe.policy()

	// the batches behind a parked one wait behind it, to keep the order of the events
	if policy == bootstrap.OverflowBlock && m.queued.Load() > 0 {
		m.parkBatch(events, parkedAt)

		return
	}

	for i, event := range events {
		select {
		case m.in <- event:
			continue
		default:
		}

		switch policy {
		case bootstrap.OverflowBlock:
			m.parkBatch(events[i:], parkedAt)

			return
		case bootstrap.OverflowDropOldest:
			m.replaceOldest(event)
		default:
			m.dropOverflow()
		}
	}
}

// parkedBatch is a batch in the waiting room, with the time its block deadline ends.
type parkedBatch struct {
	deadline time.Time
	events   []Event
}

// parkBatch puts a copy of the events in the waiting room, stamped with its deadline (the block timeout from the
// time it was offered), or drops them when it is full.
func (m *managed) parkBatch(events []Event, parkedAt time.Time) {
	m.queued.Add(1)

	select {
	case m.parked <- parkedBatch{events: slices.Clone(events), deadline: parkedAt.Add(m.pipe.BlockTimeout)}:
	default:
		m.queued.Add(-1)
		m.instance.blocked.Add(uint64(len(events)))
		m.name.stats.blocked.Add(uint64(len(events)))
	}
}

// replaceOldest queues the event in place of the oldest one when the hand-off is full.
func (m *managed) replaceOldest(event Event) {
	select {
	case <-m.in:
		m.dropOverflow()
	default:
	}

	select {
	case m.in <- event:
	default:
		m.dropOverflow()
	}
}

// park is the goroutine of the waiting room: it places the parked batches in the hand-off one after the other, each
// within the block deadline. It ends when the instance leaves the set.
func (m *managed) park() {
	for {
		// the end of the instance wins over a batch that is waiting: nothing is placed after it
		select {
		case <-m.retired:
			return
		default:
		}

		select {
		case batch := <-m.parked:
			m.place(batch.events, batch.deadline)
			m.queued.Add(-1)
		case <-m.retired:
			return
		}
	}
}

// place waits for room in the hand-off for the events of a batch, until the block deadline of the batch (stamped when
// it was parked): what
// does not fit by then is dropped and counted as a block timeout. The end of the instance ends the wait; the
// events it still holds are counted by the drain.
func (m *managed) place(events []Event, until time.Time) {
	// what is left of the block timeout of the batch, which started when it was parked
	remaining := until.Sub(m.now())
	expired := remaining <= 0

	var deadline <-chan time.Time
	if !expired {
		deadline = m.after(remaining)
	}

	for _, event := range events {
		select {
		case <-m.retired:
			return
		default:
		}

		select {
		case m.in <- event:
			continue
		default:
		}

		if !expired {
			select {
			case m.in <- event:
				continue
			case <-deadline:
				expired = true
			case <-m.retired:
				return
			}
		}

		m.instance.blocked.Add(1)
		m.name.stats.blocked.Add(1)
	}
}

// managerDropped is the number of events the manager dropped because its store was full.
func (m *managed) managerDropped() uint64 {
	if m.finalized.Load() {
		return m.mgrFinal.Load()
	}

	if counter, ok := m.mgr.(exporter.DroppedEventsCounter); ok {
		return uint64(max(counter.DroppedEvents(), 0))
	}

	return 0
}

// pending is what was handed to the instance and is neither exported nor dropped.
func (m *managed) pending() uint64 {
	gone := m.instance.exported.Load() + m.instance.handoff.Load() + m.managerDropped() +
		m.instance.blocked.Load() + m.instance.exhausted.Load()
	handed := m.instance.handed.Load()

	if handed < gone {
		return 0
	}

	return handed - gone
}

// startClose closes the hand-off, lets the goroutine of the hand-off deliver what it holds, stops the manager
// (which flushes it) and folds the counters of the instance in those of its name. It runs once, in the
// background: the caller waits for [managed.finished] as long as it wants to.
func (m *managed) startClose(ctx context.Context) {
	m.closeOnce.Do(func() {
		m.state.CompareAndSwap(stateRunning, stateDraining)

		// the drain deadline of the exporter, from now, inside the deadline of ctx: it ends the retries
		drain, cancel := context.WithTimeout(ctx, m.drain)
		stop := context.AfterFunc(drain, m.stopRetries)

		// a deadline that has passed already halts the exporter before anything of the close runs
		if drain.Err() != nil {
			m.stopRetries()
		}

		m.retire()

		go func() {
			defer cancel()
			defer stop()
			defer m.stopRetries()
			defer close(m.finished)

			// the waiting room is over: nothing sends to the hand-off any more
			m.blockers.Wait()
			close(m.in)
			<-m.consumed
			m.mgr.Stop()
			m.release(context.WithoutCancel(ctx))
			m.finalize()
		}()
	})
}

// finalize folds the instance in its name: the drops of the manager, and the events that were handed and are
// neither exported nor dropped, which can no longer be delivered, are counted as drain drops. It runs when the
// manager has stopped, however late that is, so a manager that finished after its drain was cut reconciles.
func (m *managed) finalize() {
	stats := &m.name.stats

	stats.mu.Lock()
	dropped := uint64(0)

	if counter, ok := m.mgr.(exporter.DroppedEventsCounter); ok {
		dropped = uint64(max(counter.DroppedEvents(), 0))
	}

	m.mgrFinal.Store(dropped)

	stats.mgrDropped += dropped

	m.finalized.Store(true)
	stats.mu.Unlock()

	left := m.pending()
	if left > 0 {
		stats.drain.Add(left)
	}

	m.undelivered.Store(left)

	m.state.Store(stateStopped)
}

// haltDone is closed when the drain deadline of the exporter passed (or the exporter finished closing).
func (m *managed) haltDone() <-chan struct{} { return m.def.wrapper().halt }

// isFinished says whether the instance finished closing.
func (m *managed) isFinished() bool {
	select {
	case <-m.finished:
		return true
	default:
		return false
	}
}

// release closes the exporter when it holds resources (a producer, a client): the manager flushed it, nothing
// uses it any more. A failure is logged by class only: its message could carry a secret.
func (m *managed) release(ctx context.Context) {
	closer, ok := m.def.Inner.Unseal().(io.Closer)
	if !ok {
		return
	}

	err := closer.Close()
	if err != nil {
		m.logger.LogAttrs(ctx, slog.LevelWarn, "closing the exporter failed",
			slog.String("exporter", m.def.Name), slog.String("class", string(classify(err))))
	}
}

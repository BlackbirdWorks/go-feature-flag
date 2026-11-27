package events

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/blackbirdworks/semaphore/exporter"
)

// managed is a running instance of an exporter: its definition, its engine manager, its own bounded hand-off
// and the goroutine that moves the events from the hand-off to the manager.
//
// A worker of the dispatcher never waits on a manager: it offers the events to the hand-off of each instance
// without blocking, and an instance whose hand-off is full drops (and counts) its own events. The manager may
// wait (the engine manager waits for room in its store) in the goroutine of its instance only.
type managed struct {
	mgr    FeatureManager
	logger *slog.Logger
	queue  func([]Event)
	// in is the hand-off: bounded, closed when the instance leaves the set.
	in       chan Event
	consumed chan struct{}
	finished chan struct{}
	name     *nameEntry
	instance *instanceStats
	def      ExporterDef
	// mgrFinal is the number of events the manager dropped, once it stopped.
	mgrFinal atomic.Uint64
	// undelivered is the number of events the instance could not deliver, known once it finished.
	undelivered atomic.Uint64
	closeOnce   sync.Once
	state       atomic.Int32
	batch       int
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
	inst := &managed{
		def:      *def,
		mgr:      mgr,
		name:     entry,
		instance: def.wrapper().instance,
		in:       make(chan Event, handoff),
		consumed: make(chan struct{}),
		finished: make(chan struct{}),
		batch:    batch,
		logger:   logger,
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

	go inst.run()

	return inst
}

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

// offer hands the events to the instance without blocking. What does not fit is dropped and counted against the
// instance.
func (m *managed) offer(events []Event) {
	count := uint64(len(events))
	m.instance.handed.Add(count)
	m.name.stats.handed.Add(count)

	for _, event := range events {
		select {
		case m.in <- event:
		default:
			m.instance.handoff.Add(1)
			m.name.stats.handoff.Add(1)
		}
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
	gone := m.instance.exported.Load() + m.instance.handoff.Load() + m.managerDropped()
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

		go func() {
			defer close(m.finished)

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

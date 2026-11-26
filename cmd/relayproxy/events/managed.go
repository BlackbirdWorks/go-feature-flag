package events

import (
	"sync/atomic"

	"github.com/blackbirdworks/semaphore/exporter"
)

// managed is a running exporter: its definition, its manager and its counters.
type managed struct {
	mgr   FeatureManager
	queue func([]Event)
	def   ExporterDef
	// handed counts the events given to the manager.
	handed atomic.Uint64
	// cut counts the events that were not delivered when the exporter closed.
	cut atomic.Uint64
	// overflow is the final count of the events the manager dropped, once it stopped.
	overflow atomic.Uint64
	state    atomic.Int32
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

func newManaged(def *ExporterDef, mgr FeatureManager) *managed {
	mgd := &managed{def: *def, mgr: mgr}

	if async, ok := mgr.(exporter.AsyncManager[*exporter.FeatureEvent]); ok {
		mgd.queue = async.QueueEvents
	} else {
		mgd.queue = func(batch []Event) {
			for _, event := range batch {
				mgr.AddEvent(event)
			}
		}
	}

	return mgd
}

// deliver hands a batch to the manager.
func (m *managed) deliver(batch []Event) {
	m.handed.Add(uint64(len(batch)))
	m.queue(batch)
}

// overflowCount is the number of events the manager dropped because its queue was full.
func (m *managed) overflowCount() uint64 {
	if m.state.Load() == stateRunning || m.state.Load() == stateDraining {
		if counter, ok := m.mgr.(exporter.DroppedEventsCounter); ok {
			return uint64(max(counter.DroppedEvents(), 0))
		}
	}

	return m.overflow.Load()
}

// queued is what was handed to the exporter and is neither exported nor dropped.
func (m *managed) queued() int64 {
	gone := m.def.stats.exported.Load() + m.overflowCount() + m.cut.Load()

	return max(int64(m.handed.Load())-int64(gone), 0) //nolint:gosec // event counts do not reach 2^63
}

func (m *managed) status() ExporterStatus {
	return ExporterStatus{
		Name:          m.def.Name,
		Kind:          m.def.Kind,
		State:         stateName(m.state.Load()),
		LastError:     m.def.stats.last.get(),
		Dropped:       Drops{Overflow: m.overflowCount(), Drain: m.cut.Load()},
		Queued:        m.queued(),
		Handed:        m.handed.Load(),
		Exported:      m.def.stats.exported.Load(),
		FlushFailures: m.def.stats.failures.Load(),
		Flushes:       m.def.stats.flushes.Load(),
		FlushSeconds:  float64(m.def.stats.flushNanos.Load()) / 1e9, //nolint:mnd // nanoseconds per second
	}
}

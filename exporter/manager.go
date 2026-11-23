package exporter

import (
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/thomaspoignant/go-feature-flag/utils/fflog"
)

const DefaultExporterCleanQueueInterval = 1 * time.Minute

// DefaultQueuedEventsFactor is the default hard limit of events kept in memory by a manager,
// expressed as a multiple of the largest MaxEventInMemory of its exporters.
// See Config.MaxQueuedEvents.
const DefaultQueuedEventsFactor = 4

// dropLogInterval is the minimum delay between two warnings about dropped events.
const dropLogInterval = 10 * time.Second

type Manager[T ExportableEvent] interface {
	AddEvent(event T)
	Start()
	Stop()
}

// DroppedEventsCounter is implemented by the managers that can tell how many events they had to
// drop because their queue was full (see Config.MaxQueuedEvents).
type DroppedEventsCounter interface {
	DroppedEvents() int64
}

type managerImpl[T ExportableEvent] struct {
	logger     *fflog.FFLogger
	consumers  []DataExporter[T]
	eventStore *EventStore[T]
	// nextFlushAfter (unix nano), per consumer, is the time before which the "pending events
	// reached MaxEventInMemory" trigger does not retry a flush that has just failed.
	nextFlushAfter []atomic.Int64
	lastDropLog    atomic.Int64
}

// flushRetryBackoff is the delay before the event-count trigger retries a failed flush: without
// it, a failing exporter would be called (with all the pending events) on every added event.
// The periodic flush (FlushInterval) is not affected.
const flushRetryBackoff = 1 * time.Second

func NewManager[T ExportableEvent](exporters []Config,
	exporterCleanQueueInterval time.Duration, logger *fflog.FFLogger) Manager[T] {
	if exporterCleanQueueInterval == 0 {
		// default value for the exporterCleanQueueDuration is 1 minute
		exporterCleanQueueInterval = DefaultExporterCleanQueueInterval
	}

	m := &managerImpl[T]{logger: logger}
	var maxQueued int64
	for _, exporter := range exporters {
		limit := exporter.MaxQueuedEvents
		if limit == 0 {
			maxEvents := exporter.MaxEventInMemory
			if maxEvents == 0 {
				maxEvents = defaultMaxEventInMemory
			}
			limit = maxEvents * DefaultQueuedEventsFactor
		}
		// the store is shared: it must be able to hold what its most demanding consumer asks for.
		maxQueued = max(maxQueued, limit)
	}
	evStore := NewEventStoreWithLimit[T](exporterCleanQueueInterval, maxQueued, m.onDrop)
	consumers := make([]DataExporter[T], len(exporters))
	for index, exporter := range exporters {
		consumerID := uuid.New().String()
		exp := NewDataExporter(exporter, consumerID, evStore, logger)
		consumers[index] = exp
		evStore.AddConsumer(consumerID)
	}
	m.consumers = consumers
	m.eventStore = &evStore
	m.nextFlushAfter = make([]atomic.Int64, len(consumers))
	return m
}

// onDrop logs (at most once every dropLogInterval) that events are being dropped.
func (m *managerImpl[T]) onDrop(total int64) {
	if m.logger == nil {
		return
	}
	now := time.Now().UnixNano()
	last := m.lastDropLog.Load()
	if now-last < int64(dropLogInterval) || !m.lastDropLog.CompareAndSwap(last, now) {
		return
	}
	m.logger.Error("the exporter is not keeping up, events are dropped (drop-newest)",
		slog.Int64("totalDroppedEvents", total))
}

// DroppedEvents returns the number of events dropped because the queue was full.
func (m *managerImpl[T]) DroppedEvents() int64 {
	if c, ok := (*m.eventStore).(interface{ DroppedEvents() int64 }); ok {
		return c.DroppedEvents()
	}
	return 0
}

func (m *managerImpl[T]) AddEvent(event T) {
	store := *m.eventStore
	store.Add(event)
	for i, consumer := range m.consumers {
		if !consumer.IsBulk() {
			consumer.Flush()
			continue
		}
		if time.Now().UnixNano() < m.nextFlushAfter[i].Load() {
			continue
		}

		count, err := store.GetPendingEventCount(consumer.GetConsumerID())
		if err != nil {
			m.logger.Error("error while fetching pending events", err)
			continue
		}
		if count >= consumer.GetMaxEventInMemory() {
			consumer.Flush()
			// still above the threshold: the flush failed (nothing was consumed).
			if after, err := store.GetPendingEventCount(consumer.GetConsumerID()); err == nil &&
				after >= count {
				m.nextFlushAfter[i].Store(time.Now().Add(flushRetryBackoff).UnixNano())
			}
			continue
		}
	}
}

func (m *managerImpl[T]) Start() {
	for _, consumer := range m.consumers {
		go consumer.Start()
	}
}

func (m *managerImpl[T]) Stop() {
	for _, consumer := range m.consumers {
		consumer.Stop()
	}
	// The consumers have flushed everything they had, nobody will read from the store anymore,
	// so we stop its periodic cleaning goroutine to avoid leaking it (and the events it retains)
	// for the lifetime of the process.
	(*m.eventStore).Stop()
}

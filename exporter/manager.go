package exporter

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/thomaspoignant/go-feature-flag/utils/fflog"
)

const DefaultExporterCleanQueueInterval = 1 * time.Minute

// DefaultQueuedEventsFactor is the default hard limit of events kept in memory by a manager,
// expressed as a multiple of the largest MaxEventInMemory of its exporters, with a floor of
// DefaultMinQueuedEvents so that a small MaxEventInMemory does not make a healthy but slowish
// exporter (a flush that takes some milliseconds) lose events in a burst.
// See Config.MaxQueuedEvents.
const DefaultQueuedEventsFactor = 4

// DefaultMinQueuedEvents is the floor of the default limit of queued events.
const DefaultMinQueuedEvents = 50000

// dropLogInterval is the minimum delay between two warnings about dropped events.
const dropLogInterval = 10 * time.Second

type Manager[T ExportableEvent] interface {
	AddEvent(event T)
	Start()
	Stop()
}

// AsyncManager is implemented by the managers that can add events without waiting for the
// exporters (see managerImpl.QueueEvents).
type AsyncManager[T ExportableEvent] interface {
	QueueEvents(events []T)
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
	// flushThresholds is, per consumer, the number of pending events that triggers a flush: its
	// MaxEventInMemory, capped by its queue limit (a threshold above the limit could never be reached).
	flushThresholds []int64
	lastDropLog     atomic.Int64

	// background flushes (QueueEvent), one goroutine at most per consumer
	flushRunning []atomic.Bool
	flushAgain   []atomic.Bool
	flushWg      sync.WaitGroup
	stopMu       sync.RWMutex
	stopped      bool
}

// flushRetryBackoff is the delay before the event-count trigger retries a failed flush: without
// it, a failing exporter would be called (with all the pending events) on every added event.
// The periodic flush (FlushInterval) is not affected.
const flushRetryBackoff = 1 * time.Second

func NewManager[T ExportableEvent](exporters []Config,
	exporterCleanQueueInterval time.Duration, logger *fflog.FFLogger,
) Manager[T] {
	if exporterCleanQueueInterval == 0 {
		// default value for the exporterCleanQueueDuration is 1 minute
		exporterCleanQueueInterval = DefaultExporterCleanQueueInterval
	}

	m := &managerImpl[T]{logger: logger}
	var maxQueued int64
	limits := make([]int64, len(exporters))
	for idx, exporter := range exporters {
		limit := exporter.MaxQueuedEvents
		if limit == 0 {
			maxEvents := exporter.MaxEventInMemory
			if maxEvents == 0 {
				maxEvents = defaultMaxEventInMemory
			}
			limit = max(maxEvents*DefaultQueuedEventsFactor, DefaultMinQueuedEvents)
		}
		// the store is shared: it must be able to hold what its most demanding consumer asks for.
		limits[idx] = limit
		maxQueued = max(maxQueued, limit)
	}
	evStore := NewEventStoreWithLimit[T](exporterCleanQueueInterval, maxQueued, m.onDrop)
	consumers := make([]DataExporter[T], len(exporters))
	for index, exporter := range exporters {
		consumerID := uuid.New().String()
		exp := NewDataExporter(exporter, consumerID, evStore, logger)
		consumers[index] = exp
		evStore.AddConsumer(consumerID)
		// the store is sized for the most demanding consumer: the others keep their own limit.
		if l, ok := evStore.(interface{ SetConsumerLimit(string, int64) }); ok {
			l.SetConsumerLimit(consumerID, limits[index])
		}
	}
	m.consumers = consumers
	m.eventStore = &evStore
	m.flushThresholds = make([]int64, len(consumers))
	for idx, c := range consumers {
		m.flushThresholds[idx] = min(c.GetMaxEventInMemory(), limits[idx])
	}
	m.nextFlushAfter = make([]atomic.Int64, len(consumers))
	m.flushRunning = make([]atomic.Bool, len(consumers))
	m.flushAgain = make([]atomic.Bool, len(consumers))
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

// QueueEvents is AddEvent for several events and for the callers that must not wait for the
// exporters: when the events make a consumer reach its flush threshold, the flush is done by a
// goroutine of the manager (at most one per consumer, it also flushes what arrives while it
// runs) instead of by the caller. Events are still added to the store synchronously (with one
// lock acquisition for all of them), so they are bounded by Config.MaxQueuedEvents like with
// AddEvent.
func (m *managerImpl[T]) QueueEvents(events []T) {
	if len(events) == 0 {
		return
	}
	m.addEvents(events, true)
}

func (m *managerImpl[T]) AddEvent(event T) {
	m.addEvents([]T{event}, false)
}

func (m *managerImpl[T]) addEvents(events []T, async bool) {
	store := *m.eventStore
	if batch, ok := store.(interface{ AddAll([]T) }); ok {
		batch.AddAll(events)
	} else {
		for _, event := range events {
			store.Add(event)
		}
	}
	for i, consumer := range m.consumers {
		if !consumer.IsBulk() {
			m.flush(i, async, false)
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
		if count >= m.flushThresholds[i] {
			m.flush(i, async, true)
		}
	}
}

// flush flushes the consumer, now or (async) in the background goroutine of the consumer.
func (m *managerImpl[T]) flush(i int, async, checkProgress bool) {
	if !async {
		m.flushNow(i, checkProgress)
		return
	}
	m.stopMu.RLock()
	defer m.stopMu.RUnlock()
	if m.stopped {
		return
	}
	m.flushAgain[i].Store(true)
	if !m.flushRunning[i].CompareAndSwap(false, true) {
		return // the running goroutine will see flushAgain
	}
	m.flushWg.Add(1)
	go func() {
		defer m.flushWg.Done()
		for {
			for m.flushAgain[i].Swap(false) {
				// a flush requested while the previous one was running was requested before that
				// one failed: it must not bypass the backoff that failure has just set.
				if checkProgress && time.Now().UnixNano() < m.nextFlushAfter[i].Load() {
					continue
				}
				m.flushNow(i, checkProgress)
			}
			m.flushRunning[i].Store(false)
			if !m.flushAgain[i].Load() || !m.flushRunning[i].CompareAndSwap(false, true) {
				return
			}
		}
	}()
}

// flushNow flushes the consumer in the calling goroutine. When checkProgress is true (flush
// triggered by the event count) and the flush fails, the next flush triggered by the event count is
// delayed by flushRetryBackoff; a successful flush lifts the delay.
func (m *managerImpl[T]) flushNow(i int, checkProgress bool) {
	consumer := m.consumers[i]
	f, ok := consumer.(interface{ flush() error })
	if !ok {
		consumer.Flush()
		return
	}
	if err := f.flush(); err != nil {
		if checkProgress {
			m.nextFlushAfter[i].Store(time.Now().Add(flushRetryBackoff).UnixNano())
		}
		return
	}
	m.nextFlushAfter[i].Store(0)
}

func (m *managerImpl[T]) Start() {
	for _, consumer := range m.consumers {
		go consumer.Start()
	}
}

func (m *managerImpl[T]) Stop() {
	// wait for the background flushes (and refuse new ones) before the final flush of the consumers
	m.stopMu.Lock()
	m.stopped = true
	m.stopMu.Unlock()
	m.flushWg.Wait()
	for _, consumer := range m.consumers {
		consumer.Stop()
	}
	// The consumers have flushed everything they had, nobody will read from the store anymore,
	// so we stop its periodic cleaning goroutine to avoid leaking it (and the events it retains)
	// for the lifetime of the process.
	(*m.eventStore).Stop()
}

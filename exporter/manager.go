package exporter

import (
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/blackbirdworks/semaphore/internal"
)

// DefaultExporterCleanQueueInterval is the interval at which the exporter queues are cleaned.
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

// Manager collects evaluation events and sends them to the exporters.
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
	// dropSignal wakes up reportDrops, which writes the warning outside of the callers.
	dropSignal chan struct{}
	stopCh     chan struct{}
	eventStore *EventStore[T]
	flushEnded chan struct{}
	logger     *slog.Logger
	// background flushes (QueueEvent), one goroutine at most per consumer
	flushRunning []atomic.Bool
	// flushThresholds is, per consumer, the number of pending events that triggers a flush: its
	// MaxEventInMemory, capped by its queue limit (a threshold above the limit could never be reached).
	flushThresholds []int64
	// maxBytes is, per consumer, the size of pending events that triggers a flush (0: no limit); the store keeps
	// the sizes.
	maxBytes   []int64
	flushAgain []atomic.Bool
	// flushStalled[i] is set when a handoff gave up waiting for the running flush of the consumer i,
	// until that flush ends: the next handoffs do not wait for it again.
	flushStalled []atomic.Bool
	// nextFlushAfter (unix nano), per consumer, is the time before which the "pending events
	// reached MaxEventInMemory" trigger does not retry a flush that has just failed.
	nextFlushAfter []atomic.Int64
	// flushFailures counts the failed flushes in a row of each consumer; maxFlushBackoff and clocks are its settings.
	flushFailures   []atomic.Int64
	maxFlushBackoff []time.Duration
	clocks          []func() time.Time
	consumers       []DataExporter[T]
	flushWg         sync.WaitGroup
	stopMu          sync.RWMutex
	// flushEnded is closed (and replaced) each time a background flush round ends, see waitForRoom.
	flushEndedMu sync.Mutex
	stopped      bool
}

// flushWaitBudget is the longest a handoff waits for the flush of a consumer to make room in the
// store before it drops what does not fit.
const flushWaitBudget = 200 * time.Millisecond

// flushRetryBackoff is the delay before the event-count trigger retries a failed flush: without
// it, a failing exporter would be called (with all the pending events) on every added event.
// The periodic flush (FlushInterval) is not affected.
const flushRetryBackoff = 1 * time.Second

// flushBackoffFactor is the growth of the pause with each failed flush in a row.
const flushBackoffFactor = 2

// NewManager returns a manager that sends evaluation events to the exporters.
func NewManager[T ExportableEvent](exporters []Config,
	exporterCleanQueueInterval time.Duration, logger *slog.Logger,
) Manager[T] {
	if exporterCleanQueueInterval == 0 {
		// default value for the exporterCleanQueueDuration is 1 minute
		exporterCleanQueueInterval = DefaultExporterCleanQueueInterval
	}

	mgr := &managerImpl[T]{
		logger:     internal.LoggerOrDiscard(logger),
		flushEnded: make(chan struct{}),
		stopCh:     make(chan struct{}),
		dropSignal: make(chan struct{}, 1),
	}

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

	evStore := NewEventStoreWithLimit[T](exporterCleanQueueInterval, maxQueued, mgr.onDrop)

	consumers := make([]DataExporter[T], len(exporters))
	for index, exporter := range exporters {
		consumerID := uuid.New().String()
		exp := NewDataExporter(exporter, consumerID, evStore, logger)
		consumers[index] = exp

		evStore.AddConsumer(consumerID)
		// the store is sized for the most demanding consumer: the others keep their own limit.
		if l, ok := evStore.(consumerLimiter); ok {
			l.SetConsumerLimit(consumerID, limits[index])
		}
	}

	mgr.consumers = consumers
	mgr.eventStore = &evStore

	mgr.flushThresholds = make([]int64, len(consumers))
	for idx, consumer := range consumers {
		mgr.flushThresholds[idx] = min(consumer.GetMaxEventInMemory(), limits[idx])
	}

	mgr.maxBytes = make([]int64, len(consumers))

	for idx, exporter := range exporters {
		mgr.maxBytes[idx] = max(exporter.MaxBytes, 0)
	}

	if slices.ContainsFunc(mgr.maxBytes, func(limit int64) bool { return limit > 0 }) {
		if sized, ok := evStore.(interface{ SetSizer(func(T) int) }); ok {
			sized.SetSizer(func(event T) int { return EventSize(event) })
		}
	}

	mgr.initFlushPauses(exporters)

	mgr.flushRunning = make([]atomic.Bool, len(consumers))
	mgr.flushAgain = make([]atomic.Bool, len(consumers))

	mgr.flushStalled = make([]atomic.Bool, len(consumers))
	go mgr.reportDrops()

	return mgr
}

// initFlushPauses sets up the pause after a failed flush of each consumer.
func (m *managerImpl[T]) initFlushPauses(exporters []Config) {
	m.nextFlushAfter = make([]atomic.Int64, len(exporters))
	m.flushFailures = make([]atomic.Int64, len(exporters))
	m.maxFlushBackoff = make([]time.Duration, len(exporters))
	m.clocks = make([]func() time.Time, len(exporters))

	for idx := range exporters {
		m.maxFlushBackoff[idx] = exporters[idx].MaxFlushBackoff

		m.clocks[idx] = time.Now
		if exporters[idx].Now != nil {
			m.clocks[idx] = exporters[idx].Now
		}
	}
}

// onDrop is called by the store, in the goroutine that adds the events (an evaluation for
// AddEvent): it only wakes up reportDrops, it never logs nor waits.
func (m *managerImpl[T]) onDrop(_ int64) {
	select {
	case m.dropSignal <- struct{}{}:
	default: // a warning is already pending
	}
}

// reportDrops logs (at most once every dropLogInterval) that events are being dropped. It runs in
// its own goroutine so that a slow or blocked log sink never delays the callers.
func (m *managerImpl[T]) reportDrops() {
	for {
		select {
		case <-m.dropSignal:
		case <-m.stopCh:
			return
		}

		m.logger.Error("the exporter is not keeping up, events are dropped",
			slog.Int64("totalDroppedEvents", m.DroppedEvents()))

		timer := time.NewTimer(dropLogInterval)
		select {
		case <-timer.C:
		case <-m.stopCh:
			timer.Stop()

			return
		}
	}
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
	if fit, ok := store.(interface{ AddWhileRoom([]T) int }); ok && async && len(events) > 1 {
		m.addEventsWaiting(store, fit, events)

		return
	}

	m.addAll(store, events)
	m.triggerFlushes(store, async)
}

func (*managerImpl[T]) addAll(store EventStore[T], events []T) {
	if batch, ok := store.(interface{ AddAll([]T) }); ok {
		batch.AddAll(events)

		return
	}

	for _, event := range events {
		store.Add(event)
	}
}

// addEventsWaiting adds a handoff that may be larger than the room of the store: what fits is added,
// the flush the consumers reach is started, and if the consumer that holds the most events is
// flushing the handoff waits (at most flushWaitBudget, and never for a consumer that is known to be
// stuck) for the room it will make, instead of dropping events an idle exporter could have taken.
// Only what still does not fit is added with the drop policy of the store.
func (m *managerImpl[T]) addEventsWaiting(
	store EventStore[T], fit interface{ AddWhileRoom([]T) int }, events []T,
) {
	// The wait budget and the flush back-off below measure real elapsed time (they pace goroutines
	// and tickers), so they use the real clock rather than clock.Now.
	deadline := time.Now().Add(flushWaitBudget)

	rest := events
	for len(rest) > 0 {
		added := fit.AddWhileRoom(rest)
		rest = rest[added:]

		m.triggerFlushes(store, true)

		if len(rest) == 0 || added > 0 {
			continue
		}

		if !m.waitForRoom(store, deadline) {
			// the flush we would have waited for may have ended since the last attempt.
			if added = fit.AddWhileRoom(rest); added == 0 {
				break
			}

			rest = rest[added:]

			m.triggerFlushes(store, true)
		}
	}

	if len(rest) > 0 {
		m.addAll(store, rest)
		m.triggerFlushes(store, true)
	}
}

// waitForRoom waits for the end of the running flush of the consumer with the most pending events.
// It returns false when there is nothing worth waiting for (no flush running, the consumer is
// backing off or stalled, the manager is stopping) or when the deadline is reached.
func (m *managerImpl[T]) waitForRoom(store EventStore[T], deadline time.Time) bool {
	m.flushEndedMu.Lock()
	ended := m.flushEnded
	m.flushEndedMu.Unlock()

	waitFor := m.consumersToWaitFor(store)
	if len(waitFor) == 0 {
		return false
	}

	markStalled := func() {
		for _, i := range waitFor {
			m.flushStalled[i].Store(true)
		}
	}

	remaining := time.Until(deadline)
	if remaining <= 0 {
		markStalled()

		return false
	}

	timer := time.NewTimer(remaining)
	defer timer.Stop()

	select {
	case <-ended:
		return true
	case <-timer.C:
		markStalled()

		return false
	case <-m.stopCh:
		return false
	}
}

// consumersToWaitFor returns the indexes of the consumers that hold the most pending events and are flushing
// without being stalled or backing off: the room is made by them.
func (m *managerImpl[T]) consumersToWaitFor(store EventStore[T]) []int {
	counts := make([]int64, len(m.consumers))
	most := int64(-1)

	for i, c := range m.consumers {
		count, err := store.GetPendingEventCount(c.GetConsumerID())
		if err != nil {
			count = -1
		}

		counts[i] = count
		most = max(most, count)
	}

	var waitFor []int

	for i, count := range counts {
		if count == most && most >= 0 && m.flushRunning[i].Load() && !m.flushStalled[i].Load() &&
			!m.paused(i) {
			waitFor = append(waitFor, i)
		}
	}

	return waitFor
}

// signalFlushEnded wakes up the handoffs waiting in waitForRoom.
func (m *managerImpl[T]) signalFlushEnded(i int) {
	m.flushStalled[i].Store(false)
	m.flushEndedMu.Lock()
	close(m.flushEnded)
	m.flushEnded = make(chan struct{})
	m.flushEndedMu.Unlock()
}

// triggerFlushes flushes the consumers that have reached their flush condition.
func (m *managerImpl[T]) triggerFlushes(store EventStore[T], async bool) {
	for i, consumer := range m.consumers {
		if !consumer.IsBulk() {
			m.flush(i, async, false)

			continue
		}

		if m.paused(i) {
			continue
		}

		count, err := store.GetPendingEventCount(consumer.GetConsumerID())
		if err != nil {
			m.logger.Error("error while fetching pending events", slog.Any("error", err))

			continue
		}

		if count >= m.flushThresholds[i] || m.bytesReached(store, i, consumer.GetConsumerID()) {
			m.flush(i, async, true)
		}
	}
}

// bytesReached says whether the events pending for the consumer reached its byte limit.
func (m *managerImpl[T]) bytesReached(store EventStore[T], i int, consumerID string) bool {
	if m.maxBytes[i] <= 0 {
		return false
	}

	sized, ok := store.(interface{ PendingBytes(string) int64 })

	return ok && sized.PendingBytes(consumerID) >= m.maxBytes[i]
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

	m.flushWg.Go(func() {
		for {
			for m.flushAgain[i].Swap(false) {
				// a flush requested while the previous one was running was requested before that
				// one failed: it must not bypass the backoff that failure has just set.
				if checkProgress && m.paused(i) {
					continue
				}

				m.flushNow(i, checkProgress)
				m.signalFlushEnded(i)
			}

			m.flushRunning[i].Store(false)
			m.signalFlushEnded(i)

			if !m.flushAgain[i].Load() || !m.flushRunning[i].CompareAndSwap(false, true) {
				return
			}
		}
	})
}

// flushNow flushes the consumer in the calling goroutine. When checkProgress is true (flush
// triggered by the event count) and the flush fails, the next flush triggered by the event count is
// delayed by flushRetryBackoff; a successful flush lifts the delay.
func (m *managerImpl[T]) flushNow(i int, checkProgress bool) {
	consumer := m.consumers[i]

	flusher, ok := consumer.(interface{ flushQueue() error })
	if !ok {
		consumer.Flush()

		return
	}

	err := flusher.flushQueue()
	if err != nil {
		if checkProgress {
			m.nextFlushAfter[i].Store(m.clocks[i]().Add(m.flushPause(i)).UnixNano())
		}

		return
	}

	m.nextFlushAfter[i].Store(0)
	m.flushFailures[i].Store(0)
}

// paused says whether the pause after a failed flush of the consumer is still running.
func (m *managerImpl[T]) paused(i int) bool {
	until := m.nextFlushAfter[i].Load()

	return until != 0 && m.clocks[i]().UnixNano() < until
}

// flushPause is the pause after a failed flush of the consumer: one second, doubled with each failed flush in a row
// up to the MaxFlushBackoff of the consumer.
func (m *managerImpl[T]) flushPause(i int) time.Duration {
	failures := m.flushFailures[i].Add(1)
	limit := m.maxFlushBackoff[i]

	pause := flushRetryBackoff
	for ; failures > 1 && pause < limit; failures-- {
		pause = min(pause*flushBackoffFactor, limit)
	}

	return pause
}

func (m *managerImpl[T]) Start() {
	for _, consumer := range m.consumers {
		go consumer.Start()
	}
}

func (m *managerImpl[T]) Stop() {
	// wait for the background flushes (and refuse new ones) before the final flush of the consumers
	m.stopMu.Lock()
	already := m.stopped
	m.stopped = true
	m.stopMu.Unlock()

	if !already {
		close(m.stopCh)
	}

	m.flushWg.Wait()

	for _, consumer := range m.consumers {
		consumer.Stop()
	}
	// The consumers have flushed everything they had, nobody will read from the store anymore,
	// so we stop its periodic cleaning goroutine to avoid leaking it (and the events it retains)
	// for the lifetime of the process.
	(*m.eventStore).Stop()
}

// consumerLimiter is implemented by event stores that limit the events kept for each consumer.
type consumerLimiter interface {
	SetConsumerLimit(consumerID string, limit int64)
}

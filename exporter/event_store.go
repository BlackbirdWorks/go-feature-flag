package exporter

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

const minOffset = int64(math.MinInt64)

type eventStoreImpl[T ExportableEvent] struct {
	// events is a list of events to store
	events []EventStoreItem[T]
	// mutex to protect the events and consumers
	mutex sync.RWMutex
	// consumers is a map of consumers with their name as key
	consumers map[string]*consumer
	// lastOffset is the last offset used for the Event store
	lastOffset int64
	// stopPeriodicCleaning is a channel to stop the periodic cleaning goroutine
	stopPeriodicCleaning chan struct{}
	// stopOnce guarantees that we close stopPeriodicCleaning only once, Stop() is reachable
	// from the exporter manager and directly from the tests.
	stopOnce sync.Once
	// cleanQueueInterval is the duration between each cleaning
	cleanQueueInterval time.Duration
	// maxEvents is the hard limit of events kept in the store, 0 means unlimited.
	maxEvents int64
	// dropped is the number of events rejected because the store was full.
	dropped atomic.Int64
	// onDrop is called (outside of the lock) with the total number of dropped events, each time
	// events have been dropped. It must be cheap and non-blocking.
	onDrop func(totalDropped int64)
}

// NewEventStore creates an event store without any limit on the number of events it keeps.
// Prefer NewEventStoreWithLimit for anything that must be memory stable.
func NewEventStore[T ExportableEvent](cleanQueueInterval time.Duration) EventStore[T] {
	return NewEventStoreWithLimit[T](cleanQueueInterval, 0, nil)
}

// NewEventStoreWithLimit creates an event store that keeps at most maxEvents events
// (0 means unlimited).
//
// Drop policy: when the store is full the NEW event is dropped (drop-newest). Events already
// stored are never discarded, so a batch that a consumer is exporting (or retrying) is never
// invalidated and the order of the events is preserved; the cost of a drop is O(1) and does not
// touch the consumers' offsets. Every drop is counted (see DroppedEvents) and reported to onDrop.
// A store only fills up when a consumer is not able to keep up (exporter slow or failing): the
// events lost are the ones that arrive while the exporter is unavailable, which is also what
// would be lost with a drop-oldest policy, only the time range differs.
func NewEventStoreWithLimit[T ExportableEvent](
	cleanQueueInterval time.Duration, maxEvents int64, onDrop func(totalDropped int64),
) EventStore[T] {
	store := &eventStoreImpl[T]{
		maxEvents:            maxEvents,
		onDrop:               onDrop,
		events:               make([]EventStoreItem[T], 0),
		mutex:                sync.RWMutex{},
		lastOffset:           minOffset,
		stopPeriodicCleaning: make(chan struct{}),
		cleanQueueInterval:   cleanQueueInterval,
		consumers:            make(map[string]*consumer),
	}
	go store.periodicCleanQueue()
	return store
}

type EventList[T ExportableEvent] struct {
	Events        []T
	InitialOffset int64
	NewOffset     int64
}

// EventStore is the interface to store events and consume them.
// It is a simple implementation of a queue with offsets.
type EventStore[T ExportableEvent] interface {
	// AddConsumer is adding a new consumer to the Event store.
	// note that you can't add a consumer after the Event store has been started.
	AddConsumer(consumerID string)

	// Add is adding item of type T in the Event store.
	Add(data T)

	// GetPendingEventCount is returning the number items available in the Event store for this consumer.
	GetPendingEventCount(consumerID string) (int64, error)

	// GetTotalEventCount returns the total number of events in the store.
	GetTotalEventCount() int64

	// ProcessPendingEvents is processing all the available item in the Event store for this consumer
	// with the process events function in parameter,
	ProcessPendingEvents(
		consumerID string,
		processEventsFunc func(context.Context, []T) error,
	) error

	// Stop is closing the Event store and stop the periodic cleaning.
	Stop()
}

type EventStoreItem[T ExportableEvent] struct {
	Offset int64
	Data   T
}

// consumer serializes ProcessPendingEvents calls for a single consumer.
// Lock ordering: consumer.mutex must always be acquired before eventStoreImpl.mutex.
// Never acquire consumer.mutex while holding eventStoreImpl.mutex.
type consumer struct {
	Offset int64
	// limit is the maximum number of pending events of the consumer, 0 means no limit of its own
	// (see SetConsumerLimit).
	limit int64
	mutex sync.Mutex
}

// AddConsumer is adding a new consumer to the Event store.
// note that you can't add a consumer after the Event store has been started.
func (e *eventStoreImpl[T]) AddConsumer(consumerID string) {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	e.consumers[consumerID] = &consumer{Offset: e.lastOffset}
}

// SetConsumerLimit sets the maximum number of events that the consumer may have pending. When
// it is exceeded the consumer gives up its oldest pending events (they are counted as dropped), so
// that a lagging consumer with a small limit does not keep more events than it asked for because
// another consumer, with a larger limit, shares the store. 0 removes the limit.
func (e *eventStoreImpl[T]) SetConsumerLimit(consumerID string, limit int64) {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	if c, ok := e.consumers[consumerID]; ok {
		c.limit = limit
	}
}

// ProcessPendingEvents is processing all the available item in the Event store for this consumer
// with the process events function in parameter,.
func (e *eventStoreImpl[T]) ProcessPendingEvents(
	consumerID string, processEventsFunc func(context.Context, []T) error) error {
	e.mutex.RLock()
	currentConsumer, err := e.getConsumer(consumerID)
	e.mutex.RUnlock()
	if err != nil {
		return err
	}

	currentConsumer.mutex.Lock()
	defer currentConsumer.mutex.Unlock()

	e.mutex.RLock()
	eventList := e.fetchPendingEvents(currentConsumer)
	e.mutex.RUnlock()

	if len(eventList.Events) == 0 {
		return nil
	}

	if err := processEventsFunc(context.Background(), eventList.Events); err != nil {
		return err
	}

	e.mutex.Lock()
	defer e.mutex.Unlock()
	return e.updateConsumerOffset(currentConsumer, eventList.NewOffset)
}

// GetTotalEventCount returns the total number of events in the store.
func (e *eventStoreImpl[T]) GetTotalEventCount() int64 {
	e.mutex.RLock()
	defer e.mutex.RUnlock()
	return int64(len(e.events))
}

// GetPendingEventCount is returning the number items available in the Event store for this consumer.
func (e *eventStoreImpl[T]) GetPendingEventCount(consumerID string) (int64, error) {
	e.mutex.RLock()
	defer e.mutex.RUnlock()
	consumer, err := e.getConsumer(consumerID)
	if err != nil {
		return 0, err
	}
	return e.lastOffset - consumer.Offset, nil
}

// Add is adding item of type T in the Event store.
// If the store has a limit and is full, the event is dropped and counted (see NewEventStoreWithLimit).
func (e *eventStoreImpl[T]) Add(data T) {
	e.AddAll([]T{data})
}

// AddAll adds several events with a single lock acquisition (see Add for the drop policy).
func (e *eventStoreImpl[T]) AddAll(data []T) {
	var dropped int64
	e.mutex.Lock()
	for _, d := range data {
		if e.maxEvents > 0 && int64(len(e.events)) >= e.maxEvents {
			// the events consumed by everybody but not cleaned yet do not count: release them first.
			e.cleanQueueLocked()
		}
		if e.maxEvents > 0 && int64(len(e.events)) >= e.maxEvents {
			// a consumer that is behind the others must not make the others lose events: it
			// gives up its oldest pending event instead (counted as a drop).
			dropped += e.evictLaggardsLocked()
			if int64(len(e.events)) >= e.maxEvents {
				dropped++
				continue
			}
		}
		e.lastOffset++
		e.events = append(e.events, EventStoreItem[T]{Offset: e.lastOffset, Data: d})
	}
	dropped += e.enforceConsumerLimitsLocked()
	e.mutex.Unlock()
	if dropped > 0 {
		total := e.dropped.Add(dropped)
		if e.onDrop != nil {
			e.onDrop(total)
		}
	}
}

// enforceConsumerLimitsLocked makes the consumers that have more pending events than their own
// limit skip their oldest ones. It returns the number of (consumer, event) deliveries given up.
// WARNING: please call this function only in a function that has locked the mutex first.
func (e *eventStoreImpl[T]) enforceConsumerLimitsLocked() int64 {
	var dropped int64
	for _, c := range e.consumers {
		if c.limit <= 0 {
			continue
		}
		if excess := e.lastOffset - c.Offset - c.limit; excess > 0 {
			c.Offset += excess
			dropped += excess
		}
	}
	return dropped
}

// AddWhileRoom adds the events of the beginning of data that fit in the store, without dropping
// or evicting anything, and returns how many it added (0 when the store is full, or when a
// consumer is at its own limit). It lets a caller that may wait give a consumer the time to
// export what it has before deciding to drop (see AddAll for the drop policy).
func (e *eventStoreImpl[T]) AddWhileRoom(data []T) int {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	room := int64(len(data))
	if e.maxEvents > 0 && int64(len(e.events)) >= e.maxEvents {
		e.cleanQueueLocked()
	}
	if e.maxEvents > 0 {
		room = min(room, e.maxEvents-int64(len(e.events)))
	}
	for _, c := range e.consumers {
		if c.limit > 0 {
			room = min(room, c.limit-(e.lastOffset-c.Offset))
		}
	}
	if room <= 0 {
		return 0
	}
	for _, d := range data[:room] {
		e.lastOffset++
		e.events = append(e.events, EventStoreItem[T]{Offset: e.lastOffset, Data: d})
	}
	return int(room)
}

// evictLaggardsLocked is called when the store is full. If the consumers are not all at the same
// offset, the ones that are the furthest behind skip the oldest event of the store (it is not
// delivered to them), which lets the store release it and admit a new event for the consumers that
// keep up. It returns the number of (consumer, event) deliveries given up, 0 when all the consumers
// are at the same offset: then nobody is "healthier" than the others and the new event is dropped.
// WARNING: please call this function only in a function that has locked the mutex first.
func (e *eventStoreImpl[T]) evictLaggardsLocked() int64 {
	if len(e.events) == 0 || len(e.consumers) < 2 {
		return 0
	}
	lowest, highest := int64(math.MaxInt64), int64(math.MinInt64)
	for _, c := range e.consumers {
		lowest = min(lowest, c.Offset)
		highest = max(highest, c.Offset)
	}
	if lowest == highest {
		return 0
	}
	oldest := e.events[0].Offset
	var evicted int64
	for _, c := range e.consumers {
		if c.Offset == lowest && c.Offset < oldest {
			c.Offset = oldest
			evicted++
		}
	}
	if evicted == 0 {
		return 0
	}
	e.cleanQueueLocked()
	return evicted
}

// DroppedEvents returns the number of events dropped because the store was full.
func (e *eventStoreImpl[T]) DroppedEvents() int64 {
	return e.dropped.Load()
}

// fetchPendingEvents is returning all the available item in the Event store for this consumer.
// WARNING: please call this function only in a function that has locked the mutex first.
func (e *eventStoreImpl[T]) fetchPendingEvents(currentConsumer *consumer) *EventList[T] {
	// the offsets are strictly increasing: find the first pending event with a binary search.
	start := sort.Search(len(e.events), func(i int) bool {
		return e.events[i].Offset > currentConsumer.Offset
	})
	events := make([]T, 0, len(e.events)-start)
	for _, event := range e.events[start:] {
		events = append(events, event.Data)
	}
	return &EventList[T]{
		Events:        events,
		InitialOffset: currentConsumer.Offset,
		NewOffset:     e.lastOffset,
	}
}

// getConsumer checks if the consumer exists and returns it.
func (e *eventStoreImpl[T]) getConsumer(consumerID string) (*consumer, error) {
	currentConsumer, ok := e.consumers[consumerID]
	if !ok {
		return nil, fmt.Errorf("consumer with name %s not found", consumerID)
	}
	return currentConsumer, nil
}

// updateConsumerOffset updates the offset of the consumer to the new offset.
// WARNING: please call this function only in a function that has locked the mutex first.
func (e *eventStoreImpl[T]) updateConsumerOffset(currentConsumer *consumer, offset int64) error {
	if offset > e.lastOffset {
		return fmt.Errorf(
			"invalid offset: offset %d is greater than the last offset %d",
			offset,
			e.lastOffset,
		)
	}
	// the offset may have been moved forward while the events were exported (see evictLaggardsLocked)
	currentConsumer.Offset = max(currentConsumer.Offset, offset)
	return nil
}

// cleanQueue removes all events that have been consumed by all consumers.
func (e *eventStoreImpl[T]) cleanQueue() {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	e.cleanQueueLocked()
}

// CleanQueue releases the events that every consumer has already processed. It is also done
// periodically; calling it after a successful flush keeps the memory of a healthy store small.
func (e *eventStoreImpl[T]) CleanQueue() {
	e.cleanQueue()
}

// cleanQueueLocked is cleanQueue for a caller that holds the mutex.
func (e *eventStoreImpl[T]) cleanQueueLocked() {
	if len(e.events) == 0 {
		// nothing to remove
		return
	}
	// minOffset is also the offset of a consumer that has not consumed anything yet, so it can not be
	// used as the "not set" marker of the minimum.
	consumerMinOffset := int64(math.MaxInt64)
	for _, currentConsumer := range e.consumers {
		consumerMinOffset = min(consumerMinOffset, currentConsumer.Offset)
	}
	if len(e.consumers) == 0 {
		return
	}
	if consumerMinOffset <= minOffset {
		// nothing to remove
		return
	}

	i := sort.Search(len(e.events), func(i int) bool {
		return e.events[i].Offset >= consumerMinOffset
	})
	if i == len(e.events) || e.events[i].Offset != consumerMinOffset {
		return
	}
	removed := i + 1
	remaining := len(e.events) - removed
	// A plain reslice keeps the whole backing array (and the removed events it references)
	// alive, so we release the removed events and copy the rest in a right-sized array when
	// more than half of the array is waste.
	if cap(e.events) > 1024 && remaining < cap(e.events)/2 {
		kept := make([]EventStoreItem[T], remaining, max(remaining*2, 256))
		copy(kept, e.events[removed:])
		e.events = kept
		return
	}
	var zero EventStoreItem[T]
	for j := range removed {
		e.events[j] = zero
	}
	e.events = e.events[removed:]
}

// periodicCleanQueue periodically cleans the queue.
func (e *eventStoreImpl[T]) periodicCleanQueue() {
	ticker := time.NewTicker(e.cleanQueueInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			e.cleanQueue()
		case <-e.stopPeriodicCleaning:
			return
		}
	}
}

// Stop is closing the Event store and stop the periodic cleaning.
// It is safe to call it several times.
func (e *eventStoreImpl[T]) Stop() {
	e.stopOnce.Do(func() {
		close(e.stopPeriodicCleaning)
	})
}

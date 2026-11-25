package mock

import (
	"context"
	"errors"

	"github.com/blackbirdworks/semaphore/exporter"
)

var (
	errError = errors.New("error")
)

const consumerNameError = "error"

type implMockEventStore[T exporter.ExportableEvent] struct {
	store []T
}

// NewEventStore returns an in-memory event store for tests.
func NewEventStore[T exporter.ExportableEvent]() exporter.EventStore[T] {
	store := &implMockEventStore[T]{}

	return store
}

func (*implMockEventStore[T]) AddConsumer(_ string) {
	// nothing to do
}

func (e *implMockEventStore[T]) Add(data T) {
	e.store = append(e.store, data)
}

func (e *implMockEventStore[T]) ProcessPendingEvents(consumerID string,
	processFunc func(context.Context, []T) error) error {
	if consumerID == consumerNameError {
		return errError
	}

	err := processFunc(context.TODO(), e.store)
	if err != nil {
		return err
	}

	return nil
}

func (e *implMockEventStore[T]) GetPendingEventCount(consumerName string) (int64, error) {
	if consumerName == consumerNameError {
		return 0, errError
	}

	return int64(len(e.store)), nil
}

func (e *implMockEventStore[T]) GetTotalEventCount() int64 {
	return int64(len(e.store))
}

func (*implMockEventStore[T]) Stop() {
	// nothing to do
}

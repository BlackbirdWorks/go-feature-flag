package exporter_test

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/exporter"
)

// stopStore is an event store that records its calls in order and holds the first CleanQueue until a gate opens:
// a periodic flush that is in flight while Stop is called.
type stopStore struct {
	gate    chan struct{}
	entered chan struct{}
	calls   []string
	mu      sync.Mutex
	once    sync.Once
}

func (*stopStore) AddConsumer(string)                         {}
func (*stopStore) Add(*exporter.FeatureEvent)                 {}
func (*stopStore) GetPendingEventCount(string) (int64, error) { return 0, nil }
func (*stopStore) GetTotalEventCount() int64                  { return 0 }
func (*stopStore) Stop()                                      {}

func (s *stopStore) ProcessPendingEvents(string, func(context.Context, []*exporter.FeatureEvent) error) error {
	s.record("process")

	return nil
}

func (s *stopStore) CleanQueue() {
	s.record("clean-start")

	s.once.Do(func() {
		s.entered <- struct{}{}

		<-s.gate
	})

	s.record("clean-end")
}

func (s *stopStore) record(call string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.calls = append(s.calls, call)
}

func (s *stopStore) log() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]string(nil), s.calls...)
}

// bulkExporter is an exporter that sends in bulk.
type bulkExporter struct{}

func (bulkExporter) IsBulk() bool { return true }

func (bulkExporter) Export(context.Context, *slog.Logger, []exporter.ExportableEvent) error {
	return nil
}

// signalHandler closes a channel when a record contains a text.
type signalHandler struct {
	signal chan struct{}
	once   *sync.Once
	text   string
}

func (signalHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h signalHandler) Handle(_ context.Context, record slog.Record) error { //nolint:gocritic // slog.Handler signature
	if strings.Contains(record.Message, h.text) {
		h.once.Do(func() { close(h.signal) })
	}

	return nil
}

func (h signalHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h signalHandler) WithGroup(string) slog.Handler      { return h }

// TestDataExporter_StopJoinsThePeriodicWorker holds the periodic worker in a flush, calls Stop, and checks that
// Stop waits for the worker: it does not return before the worker ended, the final flush starts after the worker
// ended, and nothing is called after Stop returned. An exporter that holds resources can be closed after Stop.
func TestDataExporter_StopJoinsThePeriodicWorker(t *testing.T) {
	t.Parallel()

	store := &stopStore{gate: make(chan struct{}), entered: make(chan struct{}, 1)}
	waiting := make(chan struct{})
	logger := slog.New(signalHandler{signal: waiting, once: &sync.Once{}, text: "waiting for the periodic flush"})

	consumer := exporter.NewDataExporter[*exporter.FeatureEvent](
		exporter.Config{Exporter: bulkExporter{}, FlushInterval: time.Millisecond}, "consumer", store, logger,
	)

	workerDone := make(chan struct{})

	go func() {
		defer close(workerDone)

		consumer.Start()
	}()

	// the first tick: the worker is in its flush, held in CleanQueue
	select {
	case <-store.entered:
	case <-time.After(20 * time.Second):
		require.FailNow(t, "timed out waiting for the periodic flush")
	}

	stopped := make(chan struct{})

	go func() {
		defer close(stopped)

		consumer.Stop()
	}()

	// Stop says it waits for the worker, and it has not returned: the worker is still held
	select {
	case <-waiting:
	case <-time.After(20 * time.Second):
		require.FailNow(t, "timed out waiting for Stop to wait for the periodic worker")
	}

	select {
	case <-stopped:
		require.FailNow(t, "Stop returned while the periodic flush was in flight")
	default:
	}

	close(store.gate)

	select {
	case <-stopped:
	case <-time.After(20 * time.Second):
		require.FailNow(t, "timed out waiting for Stop to return")
	}

	select {
	case <-workerDone:
	default:
		assert.Fail(t, "the periodic worker was still running when Stop returned")
	}

	assert.Equal(t,
		[]string{"process", "clean-start", "clean-end", "process", "clean-start", "clean-end"},
		store.log(), "the flush in flight ends first, then the final flush of Stop, and nothing after it")
}

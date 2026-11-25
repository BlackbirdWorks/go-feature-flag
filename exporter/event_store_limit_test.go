package exporter_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/testhelpers"
)

var (
	errBoom          = errors.New("boom")
	errAlwaysFailing = errors.New("always failing")
)

type droppedCounter interface{ DroppedEvents() int64 }

func Test_EventStoreWithLimit(t *testing.T) {
	t.Parallel()

	process := func(
		out *[]testhelpers.ExportableMockEvent,
		err error,
	) func(context.Context, []testhelpers.ExportableMockEvent) error {
		return func(_ context.Context, events []testhelpers.ExportableMockEvent) error {
			if err != nil {
				return err
			}

			*out = append(*out, events...)

			return nil
		}
	}

	tests := []struct {
		consumeErr     error
		name           string
		wantConsumeErr string
		wantOut        []string // events exported by the (successful) consume, in order
		// steps are executed in order against a store limited to limit events
		limit       int64
		adds        int
		addsAfter   int
		wantStored  int64
		wantDropped int64
		consume     bool // consume everything after the adds
	}{
		{name: "no limit keeps everything", limit: 0, adds: 50, wantStored: 50},
		{name: "under the limit", limit: 10, adds: 10, wantStored: 10},
		{name: "over the limit drops the newest", limit: 10, adds: 25, wantStored: 10, wantDropped: 15},
		{
			name: "failing consumer never grows the store", limit: 10, adds: 25,
			consume: true, consumeErr: errBoom, addsAfter: 25, wantStored: 10, wantDropped: 40,
			wantConsumeErr: "boom",
		},
		{
			name: "consumed events free room", limit: 10, adds: 10, consume: true, addsAfter: 10,
			wantStored: 10, wantDropped: 0,
			wantOut: []string{"e0", "e1", "e2", "e3", "e4", "e5", "e6", "e7", "e8", "e9"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var (
				onDropTotal int64
				mu          sync.Mutex
			)

			store := exporter.NewEventStoreWithLimit[testhelpers.ExportableMockEvent](
				time.Hour, tt.limit, func(total int64) { mu.Lock(); onDropTotal = total; mu.Unlock() })
			defer store.Stop()

			store.AddConsumer("c")

			for i := range tt.adds {
				store.Add(testhelpers.NewExportableMockEvent(fmt.Sprintf("e%d", i)))
			}

			var out []testhelpers.ExportableMockEvent
			if tt.consume {
				err := store.ProcessPendingEvents("c", process(&out, tt.consumeErr))
				if tt.wantConsumeErr != "" {
					require.EqualError(t, err, tt.wantConsumeErr)
				} else {
					require.NoError(t, err)
				}
			}

			gotOut := slices.Collect(func(yield func(string) bool) {
				for _, e := range out {
					if !yield(e.GetKey()) {
						return
					}
				}
			})
			assert.Equal(t, tt.wantOut, gotOut)

			for i := range tt.addsAfter {
				store.Add(testhelpers.NewExportableMockEvent(fmt.Sprintf("a%d", i)))
			}

			assert.Equal(t, tt.wantStored, store.GetTotalEventCount())
			dc, ok := store.(droppedCounter)
			require.True(t, ok)
			assert.Equal(t, tt.wantDropped, dc.DroppedEvents())
			mu.Lock()
			assert.Equal(t, tt.wantDropped, onDropTotal)
			mu.Unlock()
		})
	}
}

// the events that are kept are the oldest ones and are delivered in order, once.
func Test_EventStoreWithLimit_KeepsOldestInOrder(t *testing.T) {
	t.Parallel()

	store := exporter.NewEventStoreWithLimit[testhelpers.ExportableMockEvent](time.Hour, 3, nil)
	defer store.Stop()

	store.AddConsumer("c")

	for i := range 6 {
		store.Add(testhelpers.NewExportableMockEvent(fmt.Sprintf("e%d", i)))
	}

	var got []string

	require.NoError(
		t,
		store.ProcessPendingEvents("c", func(_ context.Context, ev []testhelpers.ExportableMockEvent) error {
			for _, e := range ev {
				got = append(got, e.GetKey())
			}

			return nil
		}),
	)
	assert.Equal(t, []string{"e0", "e1", "e2"}, got)
}

func Test_EventStoreWithLimit_ConcurrentAddsStayBounded(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		limit       int64
		workers     int
		perW        int
		wantStored  int64
		wantDropped int64
	}{
		{name: "small limit", limit: 100, workers: 8, perW: 2000, wantStored: 100, wantDropped: 15900},
		{name: "limit above the volume", limit: 100000, workers: 4, perW: 1000, wantStored: 4000, wantDropped: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := exporter.NewEventStoreWithLimit[testhelpers.ExportableMockEvent](time.Hour, tt.limit, nil)
			defer store.Stop()

			store.AddConsumer("c")

			var wg sync.WaitGroup
			for range tt.workers {
				wg.Go(func() {
					for range tt.perW {
						store.Add(testhelpers.NewExportableMockEvent("e"))
						assert.LessOrEqual(t, store.GetTotalEventCount(), tt.limit)
					}
				})
			}

			wg.Wait()
			assert.Equal(t, tt.wantStored, store.GetTotalEventCount())
			dc, ok := store.(droppedCounter)
			require.True(t, ok)
			assert.Equal(t, tt.wantDropped, dc.DroppedEvents())
		})
	}
}

// a consumer that always fails must neither grow the store, nor be called on every event.
func Test_Manager_FailingExporterIsBounded(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		maxMem  int64
		maxQ    int64
		wantCap int64
	}{
		{name: "default limit is 4x MaxEventInMemory", maxMem: 20000, wantCap: 80000},
		{name: "default limit has a floor", maxMem: 10, wantCap: 50000},
		{name: "explicit limit", maxMem: 10, maxQ: 25, wantCap: 25},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			exp := &failingExporter{}

			mgr := exporter.NewManager[*exporter.FeatureEvent]([]exporter.Config{{
				Exporter: exp, FlushInterval: time.Hour, MaxEventInMemory: tt.maxMem, MaxQueuedEvents: tt.maxQ,
			}}, time.Hour, nil)
			defer mgr.Stop()

			const totalEvents = 100000
			// The flush backoff compares time.Now().UnixNano() (wall clock) against its deadline, on
			// purpose: it paces real goroutines, and the package clock is frozen by TestMain. Measure
			// the elapsed time on that same wall clock. A backwards wall-clock step during the test
			// is out of scope (a negative elapsed is treated as 0).
			begin := time.Now().UnixNano()

			for range totalEvents {
				mgr.AddEvent(&exporter.FeatureEvent{Key: "k"})
			}

			elapsed := max(time.Duration(time.Now().UnixNano()-begin), 0)
			dc, ok := mgr.(exporter.DroppedEventsCounter)
			require.True(t, ok)
			assert.Equal(t, int64(totalEvents)-tt.wantCap, dc.DroppedEvents())
			// Without backoff the exporter would have been called for every event above the threshold.
			// A failed flush delays the next event-count flush by flushRetryBackoff (1s) from the end
			// of the failed attempt, so two attempts start at least 1s apart: during elapsed, at most
			// one attempt per whole second after the first one. The bound is exact whatever the
			// speed of the machine (a slow runner gets more windows, never more attempts per window).
			assert.LessOrEqual(t, int64(exp.calls.Load()), 1+int64(elapsed/time.Second))
		})
	}
}

type failingExporter struct{ calls atomic.Int32 }

func (*failingExporter) IsBulk() bool { return true }
func (f *failingExporter) Export(context.Context, *slog.Logger, []exporter.ExportableEvent) error {
	f.calls.Add(1)

	return errAlwaysFailing
}

type queueCleaner interface{ CleanQueue() }

// events are only released once every consumer has processed them, even when one of the
// consumers has not consumed anything yet (its offset is then the initial offset).
func Test_EventStore_CleanQueueWaitsForAllConsumers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		consumeBoth  bool
		wantAfterGet int64
	}{
		{name: "one consumer behind keeps the events", consumeBoth: false, wantAfterGet: 5},
		{name: "all consumers done releases the events", consumeBoth: true, wantAfterGet: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := exporter.NewEventStore[testhelpers.ExportableMockEvent](time.Hour)
			defer store.Stop()

			store.AddConsumer("a")
			store.AddConsumer("b")

			for range 5 {
				store.Add(testhelpers.NewExportableMockEvent("e"))
			}

			noop := func(context.Context, []testhelpers.ExportableMockEvent) error { return nil }
			require.NoError(t, store.ProcessPendingEvents("a", noop))

			if tt.consumeBoth {
				require.NoError(t, store.ProcessPendingEvents("b", noop))
			}

			qc, ok := store.(queueCleaner)
			require.True(t, ok)
			qc.CleanQueue()
			assert.Equal(t, tt.wantAfterGet, store.GetTotalEventCount())
		})
	}
}

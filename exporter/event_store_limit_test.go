package exporter_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/testutils"
	"github.com/blackbirdworks/semaphore/utils/fflog"
)

type droppedCounter interface{ DroppedEvents() int64 }

func Test_EventStoreWithLimit(t *testing.T) {
	t.Parallel()
	process := func(out *[]testutils.ExportableMockEvent, err error) func(context.Context, []testutils.ExportableMockEvent) error {
		return func(_ context.Context, events []testutils.ExportableMockEvent) error {
			if err != nil {
				return err
			}
			*out = append(*out, events...)

			return nil
		}
	}

	tests := []struct {
		name string
		// steps are executed in order against a store limited to limit events
		limit          int64
		adds           int
		consume        bool // consume everything after the adds
		consumeErr     error
		wantConsumeErr string
		addsAfter      int
		wantStored     int64
		wantDropped    int64
		wantOut        []string // events exported by the (successful) consume, in order
	}{
		{name: "no limit keeps everything", limit: 0, adds: 50, wantStored: 50},
		{name: "under the limit", limit: 10, adds: 10, wantStored: 10},
		{name: "over the limit drops the newest", limit: 10, adds: 25, wantStored: 10, wantDropped: 15},
		{
			name: "failing consumer never grows the store", limit: 10, adds: 25,
			consume: true, consumeErr: errors.New("boom"), addsAfter: 25, wantStored: 10, wantDropped: 40,
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
			var onDropTotal int64
			var mu sync.Mutex
			store := exporter.NewEventStoreWithLimit[testutils.ExportableMockEvent](
				time.Hour, tt.limit, func(total int64) { mu.Lock(); onDropTotal = total; mu.Unlock() })
			defer store.Stop()
			store.AddConsumer("c")
			for i := range tt.adds {
				store.Add(testutils.NewExportableMockEvent(fmt.Sprintf("e%d", i)))
			}
			var out []testutils.ExportableMockEvent
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
				store.Add(testutils.NewExportableMockEvent(fmt.Sprintf("a%d", i)))
			}
			assert.Equal(t, tt.wantStored, store.GetTotalEventCount())
			assert.Equal(t, tt.wantDropped, store.(droppedCounter).DroppedEvents())
			mu.Lock()
			assert.Equal(t, tt.wantDropped, onDropTotal)
			mu.Unlock()
		})
	}
}

// the events that are kept are the oldest ones and are delivered in order, once.
func Test_EventStoreWithLimit_KeepsOldestInOrder(t *testing.T) {
	t.Parallel()
	store := exporter.NewEventStoreWithLimit[testutils.ExportableMockEvent](time.Hour, 3, nil)
	defer store.Stop()
	store.AddConsumer("c")
	for i := range 6 {
		store.Add(testutils.NewExportableMockEvent(fmt.Sprintf("e%d", i)))
	}
	var got []string
	require.NoError(
		t,
		store.ProcessPendingEvents("c", func(_ context.Context, ev []testutils.ExportableMockEvent) error {
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
		name    string
		limit   int64
		workers int
		perW    int
	}{
		{name: "small limit", limit: 100, workers: 8, perW: 2000},
		{name: "limit above the volume", limit: 100000, workers: 4, perW: 1000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store := exporter.NewEventStoreWithLimit[testutils.ExportableMockEvent](time.Hour, tt.limit, nil)
			defer store.Stop()
			store.AddConsumer("c")
			var wg sync.WaitGroup
			for range tt.workers {
				wg.Go(func() {
					for range tt.perW {
						store.Add(testutils.NewExportableMockEvent("e"))
						assert.LessOrEqual(t, store.GetTotalEventCount(), tt.limit)
					}
				})
			}
			wg.Wait()
			total := int64(tt.workers * tt.perW)
			assert.Equal(t, total, store.GetTotalEventCount()+store.(droppedCounter).DroppedEvents())
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
			m := exporter.NewManager[exporter.FeatureEvent]([]exporter.Config{{
				Exporter: exp, FlushInterval: time.Hour, MaxEventInMemory: tt.maxMem, MaxQueuedEvents: tt.maxQ,
			}}, time.Hour, nil)
			defer m.Stop()
			const n = 100000
			for range n {
				m.AddEvent(exporter.FeatureEvent{Key: "k"})
			}
			assert.Equal(t, int64(n)-tt.wantCap, m.(exporter.DroppedEventsCounter).DroppedEvents())
			// without backoff the exporter would have been called for every event above the threshold.
			assert.Less(t, exp.calls.Load(), int32(5))
		})
	}
}

type failingExporter struct{ calls atomic.Int32 }

func (f *failingExporter) IsBulk() bool { return true }
func (f *failingExporter) Export(context.Context, *fflog.FFLogger, []exporter.ExportableEvent) error {
	f.calls.Add(1)

	return errors.New("always failing")
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
			store := exporter.NewEventStore[testutils.ExportableMockEvent](time.Hour)
			defer store.Stop()
			store.AddConsumer("a")
			store.AddConsumer("b")
			for range 5 {
				store.Add(testutils.NewExportableMockEvent("e"))
			}
			noop := func(context.Context, []testutils.ExportableMockEvent) error { return nil }
			require.NoError(t, store.ProcessPendingEvents("a", noop))
			if tt.consumeBoth {
				require.NoError(t, store.ProcessPendingEvents("b", noop))
			}
			store.(queueCleaner).CleanQueue()
			assert.Equal(t, tt.wantAfterGet, store.GetTotalEventCount())
		})
	}
}

package exporter_test

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/testhelpers"
)

type batchAdder interface {
	AddAll([]testhelpers.ExportableMockEvent)
}

func Test_EventStore_AddAll(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		batches     []int
		limit       int64
		wantStored  int64
		wantDropped int64
	}{
		{name: "unlimited", limit: 0, batches: []int{3, 4}, wantStored: 7},
		{
			name:        "batch crossing the limit keeps the first events",
			limit:       5,
			batches:     []int{3, 4},
			wantStored:  5,
			wantDropped: 2,
		},
		{name: "empty batch", limit: 5, batches: []int{0}, wantStored: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := exporter.NewEventStoreWithLimit[testhelpers.ExportableMockEvent](time.Hour, tt.limit, nil)
			defer store.Stop()

			store.AddConsumer("c")

			for _, n := range tt.batches {
				batch := make([]testhelpers.ExportableMockEvent, n)
				for i := range batch {
					batch[i] = testhelpers.NewExportableMockEvent("e")
				}

				ba, ok := store.(batchAdder)
				require.True(t, ok)
				ba.AddAll(batch)
			}

			assert.Equal(t, tt.wantStored, store.GetTotalEventCount())
			dc, ok := store.(droppedCounter)
			require.True(t, ok)
			assert.Equal(t, tt.wantDropped, dc.DroppedEvents())

			pending, err := store.GetPendingEventCount("c")
			require.NoError(t, err)
			assert.Equal(t, tt.wantStored, pending, "offsets are contiguous: dropped events use none")
		})
	}
}

type countingExporter struct {
	events atomic.Int64
	calls  atomic.Int64
	bulk   bool
	fail   bool
}

func (c *countingExporter) IsBulk() bool { return c.bulk }
func (c *countingExporter) Export(_ context.Context, _ *slog.Logger, evts []exporter.ExportableEvent) error {
	c.calls.Add(1)

	if c.fail {
		return assert.AnError
	}

	c.events.Add(int64(len(evts)))

	return nil
}

// QueueEvents never flushes in the caller, flushes in the background when a threshold is
// reached, and Stop waits for the background flushes.
func Test_Manager_QueueEvents(t *testing.T) {
	t.Parallel()

	tests := []struct {
		exp        *countingExporter
		name       string
		maxMem     int64
		maxQueued  int64
		batches    int
		batchSize  int
		maxCalls   int64
		wantAll    bool
		wantDroped bool
	}{
		{
			name:      "bulk exporter, threshold reached",
			exp:       &countingExporter{bulk: true},
			maxMem:    50,
			batches:   20,
			batchSize: 25,
			wantAll:   true,
		},
		{
			name:      "non bulk exporter",
			exp:       &countingExporter{bulk: false},
			maxMem:    50,
			batches:   20,
			batchSize: 3,
			wantAll:   true,
		},
		{name: "failing exporter is retried with a backoff and bounded", exp: &countingExporter{bulk: true, fail: true},
			maxMem: 10, maxQueued: 100, batches: 200, batchSize: 5, maxCalls: 5, wantDroped: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mgr := exporter.NewManager[*exporter.FeatureEvent]([]exporter.Config{{
				Exporter: tt.exp, FlushInterval: time.Hour, MaxEventInMemory: tt.maxMem, MaxQueuedEvents: tt.maxQueued,
			}}, time.Hour, nil)
			asyncMgr, ok := mgr.(exporter.AsyncManager[*exporter.FeatureEvent])
			require.True(t, ok)

			total := 0

			for range tt.batches {
				batch := make([]*exporter.FeatureEvent, tt.batchSize)
				asyncMgr.QueueEvents(batch)

				total += tt.batchSize
			}

			mgr.Stop()

			if tt.wantAll {
				assert.Equal(t, int64(total), tt.exp.events.Load())
			}

			if tt.maxCalls > 0 {
				assert.LessOrEqual(t, tt.exp.calls.Load(), tt.maxCalls)
			}

			if tt.wantDroped {
				dc, dcOK := mgr.(exporter.DroppedEventsCounter)
				require.True(t, dcOK)
				assert.Positive(t, dc.DroppedEvents())
			}
			// events queued after Stop are only stored: no goroutine is started
			asyncMgr.QueueEvents(make([]*exporter.FeatureEvent, 3))
		})
	}
}

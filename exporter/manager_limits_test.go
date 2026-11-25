package exporter_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/utils/fflog"
)

// outageExporter fails until recovered is set, then counts what it receives.
type outageExporter struct {
	recovered atomic.Bool
	events    atomic.Int64
}

func (e *outageExporter) IsBulk() bool { return true }
func (e *outageExporter) Export(_ context.Context, _ *fflog.FFLogger, ev []exporter.ExportableEvent) error {
	if !e.recovered.Load() {
		return assert.AnError
	}

	e.events.Add(int64(len(ev)))

	return nil
}

// The MaxQueuedEvents of an exporter is the backlog it can have: an exporter with a small limit
// that cannot flush must not keep more events than its limit because another exporter, healthy,
// has a larger one.
func Test_Manager_QueueLimitIsPerExporter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		smallLimit   int64
		largeLimit   int64
		events       int
		async        bool
		wantSmallMax int64
		wantLarge    int64
		wantDropped  int64
	}{
		{name: "AddEvent", smallLimit: 20, largeLimit: 1000, events: 200,
			wantSmallMax: 20, wantLarge: 200, wantDropped: 180},
		{name: "QueueEvents", async: true, smallLimit: 20, largeLimit: 1000, events: 200,
			wantSmallMax: 20, wantLarge: 200, wantDropped: 180},
		{name: "same limits", smallLimit: 100, largeLimit: 100, events: 100,
			wantSmallMax: 100, wantLarge: 100, wantDropped: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// the small exporter is down: its events wait for it to recover (final flush).
			small, large := &outageExporter{}, &countingExporter{bulk: true}
			mgr := exporter.NewManager[exporter.FeatureEvent]([]exporter.Config{
				{Exporter: small, FlushInterval: time.Hour, MaxEventInMemory: 10, MaxQueuedEvents: tt.smallLimit},
				{Exporter: large, FlushInterval: time.Hour, MaxEventInMemory: 100000, MaxQueuedEvents: tt.largeLimit},
			}, time.Hour, nil)
			am, ok := mgr.(exporter.AsyncManager[exporter.FeatureEvent])
			require.True(t, ok)

			for range tt.events {
				if tt.async {
					am.QueueEvents(make([]exporter.FeatureEvent, 1))
				} else {
					mgr.AddEvent(exporter.FeatureEvent{})
				}
			}

			dc, ok := mgr.(exporter.DroppedEventsCounter)
			require.True(t, ok)

			dropped := dc.DroppedEvents()

			small.recovered.Store(true)
			mgr.Stop()
			assert.LessOrEqual(t, small.events.Load(), tt.wantSmallMax, "the small exporter keeps at most its limit")
			assert.Equal(t, tt.wantLarge, large.events.Load(), "the other exporter loses nothing")
			assert.Equal(t, tt.wantDropped, dropped, "every event given up is counted")
		})
	}
}

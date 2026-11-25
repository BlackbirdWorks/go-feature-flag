package exporter_test

import (
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/testhelpers/slogutil"
)

// Adding events must never wait for the log sink: the warning about the dropped events is written
// by a goroutine of the manager.
func Test_Manager_DropWarningDoesNotBlockAddEvent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		async        bool
		wantDropping bool
	}{
		{name: "AddEvent", wantDropping: true},
		{name: "QueueEvents", async: true, wantDropping: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sink := slogutil.NewGatedHandler(slog.DiscardHandler, "dropped")
			defer sink.Release()

			logger := slog.New(sink)
			// the exporter is down: the store fills up and the events are dropped.
			mgr := exporter.NewManager[*exporter.FeatureEvent]([]exporter.Config{{
				Exporter: &outageExporter{}, FlushInterval: time.Hour, MaxEventInMemory: 5, MaxQueuedEvents: 5,
			}}, time.Hour, logger)

			done := make(chan struct{})
			asyncMgr, ok := mgr.(exporter.AsyncManager[*exporter.FeatureEvent])
			require.True(t, ok)

			go func() {
				defer close(done)

				for range 100 {
					if tt.async {
						asyncMgr.QueueEvents(make([]*exporter.FeatureEvent, 2))
					} else {
						mgr.AddEvent(&exporter.FeatureEvent{})
					}
				}
			}()

			select {
			case <-done:
			case <-time.After(time.Minute):
				require.Fail(t, "adding events is blocked by the log sink")
			}

			dc, ok := mgr.(exporter.DroppedEventsCounter)
			require.True(t, ok)
			assert.Equal(t, tt.wantDropping, dc.DroppedEvents() > 0)
			// the warning is still written, by another goroutine
			select {
			case <-sink.Entered():
			case <-time.After(time.Minute):
				require.Fail(t, "the drop warning was not written")
			}

			sink.Release()
			mgr.Stop()
		})
	}
}

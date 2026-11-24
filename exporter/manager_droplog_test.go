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
	"github.com/blackbirdworks/semaphore/utils/fflog"
)

// gatedSink is a log handler that blocks the records about dropped events until gate is closed.
type gatedSink struct {
	inner   slog.Handler
	gate    chan struct{}
	entered chan struct{}
	once    sync.Once
}

func newGatedSink() *gatedSink {
	return &gatedSink{
		inner:   slog.DiscardHandler,
		gate:    make(chan struct{}),
		entered: make(chan struct{}),
	}
}
func (g *gatedSink) Enabled(context.Context, slog.Level) bool { return true }
func (g *gatedSink) Handle(ctx context.Context, r slog.Record) error {
	if strings.Contains(r.Message, "dropped") {
		g.once.Do(func() { close(g.entered) })
		<-g.gate
	}

	return g.inner.Handle(ctx, r)
}
func (g *gatedSink) WithAttrs(_ []slog.Attr) slog.Handler { return g }
func (g *gatedSink) WithGroup(string) slog.Handler        { return g }

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
			sink := newGatedSink()
			released := false
			release := func() {
				if !released {
					released = true
					close(sink.gate)
				}
			}
			defer release()
			logger := &fflog.FFLogger{LeveledLogger: slog.New(sink)}
			// the exporter is down: the store fills up and the events are dropped.
			m := exporter.NewManager[exporter.FeatureEvent]([]exporter.Config{{
				Exporter: &outageExporter{}, FlushInterval: time.Hour, MaxEventInMemory: 5, MaxQueuedEvents: 5,
			}}, time.Hour, logger)

			done := make(chan struct{})
			go func() {
				defer close(done)
				for range 100 {
					if tt.async {
						m.(exporter.AsyncManager[exporter.FeatureEvent]).QueueEvents(make([]exporter.FeatureEvent, 2))
					} else {
						m.AddEvent(exporter.FeatureEvent{})
					}
				}
			}()
			select {
			case <-done:
			case <-time.After(time.Minute):
				require.Fail(t, "adding events is blocked by the log sink")
			}
			assert.Equal(t, tt.wantDropping, m.(exporter.DroppedEventsCounter).DroppedEvents() > 0)
			// the warning is still written, by another goroutine
			select {
			case <-sink.entered:
			case <-time.After(time.Minute):
				require.Fail(t, "the drop warning was not written")
			}
			release()
			m.Stop()
		})
	}
}

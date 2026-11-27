package events_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/events"
	"github.com/blackbirdworks/semaphore/exporter"
)

// discardManager takes the events and keeps nothing: the cost measured is the one of the dispatcher.
type discardManager struct{}

func (discardManager) AddEvent(*exporter.FeatureEvent)      {}
func (discardManager) QueueEvents([]*exporter.FeatureEvent) {}
func (discardManager) Start()                               {}
func (discardManager) Stop()                                {}

func benchDispatcher(b *testing.B, exporters int, gate chan struct{}) *events.Dispatcher {
	b.Helper()

	newManager := func(string, exporter.Config, *slog.Logger) events.FeatureManager {
		if gate != nil {
			return &fakeManager{gate: gate}
		}

		return discardManager{}
	}

	disp := events.NewDispatcher(&events.DispatcherConfig{NewManager: newManager, QueueSize: 1 << 16})
	require.NoError(b, disp.Start(context.Background()))

	names := []string{"a", "b", "c", "d"}
	_, err := disp.Apply(context.Background(), defs(names[:exporters]...))
	require.NoError(b, err)

	b.Cleanup(func() {
		if gate != nil {
			close(gate)
		}

		_, _ = disp.Shutdown(canceled())
	})

	return disp
}

func BenchmarkCollect(b *testing.B) {
	event := ev(1)

	b.Run("no exporter", func(b *testing.B) {
		disp := benchDispatcher(b, 0, nil)

		b.ReportAllocs()
		b.ResetTimer()

		for b.Loop() {
			disp.Collect(event)
		}
	})

	b.Run("one exporter", func(b *testing.B) {
		disp := benchDispatcher(b, 1, nil)

		b.ReportAllocs()
		b.ResetTimer()

		for b.Loop() {
			disp.Collect(event)
		}
	})

	b.Run("one exporter parallel", func(b *testing.B) {
		disp := benchDispatcher(b, 1, nil)

		b.ReportAllocs()
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				disp.Collect(event)
			}
		})
	})

	b.Run("full queue", func(b *testing.B) {
		disp := benchDispatcher(b, 1, make(chan struct{}))

		for disp.Collect(event) { // the worker blocks in the gated manager, the channel fills up
		}

		b.ReportAllocs()
		b.ResetTimer()

		for b.Loop() {
			disp.Collect(event)
		}
	})
}

package events_test

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/events"
	"github.com/blackbirdworks/semaphore/exporter"
)

// gated is an engine exporter whose first call waits for the gate; it counts its calls.
type gated struct {
	gate    chan struct{}
	entered chan struct{}
	calls   atomic.Int64
}

func (*gated) IsBulk() bool { return true }

func (g *gated) Export(_ context.Context, _ *slog.Logger, _ []exporter.ExportableEvent) error {
	if g.calls.Add(1) == 1 {
		g.entered <- struct{}{}

		<-g.gate
	}

	return nil
}

// TestDispatcher_NoAttemptAfterTheDrainDeadline: once the drain deadline of an exporter passed, no new attempt starts
// (the one in flight may finish): the batches that were not sent are counted as drain drops.
func TestDispatcher_NoAttemptAfterTheDrainDeadline(t *testing.T) {
	t.Parallel()

	size := int64(exporter.EventSize(ev(0)))

	tests := []struct {
		name          string
		retry         events.RetryPolicy
		wantExported  uint64
		wantDrain     uint64
		wantCalls     int64
		wantDropTotal uint64
	}{
		{name: "retry off", wantCalls: 1, wantExported: 2, wantDrain: 4, wantDropTotal: 4},
		{
			name: "retry on", retry: events.RetryPolicy{MaxAttempts: 3, InitialBackoff: time.Millisecond},
			wantCalls: 1, wantExported: 2, wantDrain: 4, wantDropTotal: 4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			const count = 6

			sink := &gated{gate: make(chan struct{}), entered: make(chan struct{}, 1)}

			t.Cleanup(func() { safeClose(sink.gate) })

			disp := newDispatcher(t, &events.DispatcherConfig{After: newTimers(true).after, Rand: halfRand})
			require.NoError(t, disp.Start(context.Background()))

			expDef := events.NewExporterDef("a", kindFile, "v1", sink, exporter.Config{
				MaxBytes: 2 * size, FlushInterval: time.Hour, MaxEventInMemory: count,
			})
			expDef.Pipeline = events.Pipeline{MaxBytes: 2 * size, DrainTimeout: time.Nanosecond, Retry: tt.retry}

			_, err := disp.Apply(context.Background(), []events.ExporterDef{expDef})
			require.NoError(t, err)

			for i := range count {
				require.True(t, disp.Collect(ev(i)))
			}

			await(t, sink.entered, "the first batch to be in flight")

			report, err := disp.Shutdown(bounded(t))
			require.NoError(t, err)
			require.True(t, report.Drained[0].Cut, "the drain deadline passed")

			close(sink.gate)
			require.NoError(t, disp.WaitDrains(bounded(t)))

			got := statusOf(t, new(disp.Status()), "a")
			assert.Equal(t, tt.wantCalls, sink.calls.Load(), "no attempt started after the deadline")
			assert.Equal(t, tt.wantExported, got.Exported)
			assert.Equal(t, tt.wantDrain, got.Dropped.Drain)
			assert.Equal(t, tt.wantDropTotal, got.Dropped.Total())
			assert.Equal(t, uint64(count), got.Handed)
		})
	}
}

// TestDispatcher_NoRetryFollowsOnExhausted: with MaxAttempts 0 a batch gets one attempt and then the onExhausted
// rule: drop counts it as retry_exhausted, keep leaves it to the engine manager (it is exported by its final flush).
func TestDispatcher_NoRetryFollowsOnExhausted(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		retry         events.RetryPolicy
		wantExhausted uint64
		wantExported  uint64
		wantDrain     uint64
	}{
		{name: "drop", retry: events.RetryPolicy{Drop: true}, wantExhausted: 1, wantExported: 0},
		{name: "keep", retry: events.RetryPolicy{}, wantExhausted: 0, wantExported: 0, wantDrain: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sink := &flaky{failFirst: -1}
			disp := newDispatcher(t, &events.DispatcherConfig{After: newTimers(true).after, Rand: halfRand})
			require.NoError(t, disp.Start(context.Background()))

			expDef := events.NewExporterDef("a", kindFile, "v1", sink, exporter.Config{MaxEventInMemory: 1})
			expDef.Pipeline = events.Pipeline{Retry: tt.retry}

			_, err := disp.Apply(context.Background(), []events.ExporterDef{expDef})
			require.NoError(t, err)
			require.True(t, disp.Collect(ev(0)))

			_, err = disp.Shutdown(bounded(t))
			require.NoError(t, err)

			got := statusOf(t, new(disp.Status()), "a")
			assert.Equal(t, tt.wantExhausted, got.Dropped.RetryExhausted)
			assert.Equal(t, tt.wantExported, got.Exported)
			assert.Equal(t, tt.wantDrain, got.Dropped.Drain)
			assert.Equal(t, got.Handed, got.Exported+got.Dropped.Total(), "every event is counted")
		})
	}
}

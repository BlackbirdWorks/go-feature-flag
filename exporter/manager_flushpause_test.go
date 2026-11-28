package exporter_test

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/exporter"
)

// stepClock is a clock that the test moves.
type stepClock struct {
	at time.Time
	mu sync.Mutex
}

func (c *stepClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.at
}

func (c *stepClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.at = c.at.Add(d)
}

// downThenUp fails until recovered is set; it counts its calls.
type downThenUp struct {
	recovered atomic.Bool
	calls     atomic.Int64
}

func (*downThenUp) IsBulk() bool { return true }

func (e *downThenUp) Export(_ context.Context, _ *slog.Logger, _ []exporter.ExportableEvent) error {
	e.calls.Add(1)

	if !e.recovered.Load() {
		return assert.AnError
	}

	return nil
}

// TestManager_FlushPauseGrowsWhileFailing: an event every second for 60 simulated seconds against a dead exporter. The
// pause between the flushes the event count triggers is one second without MaxFlushBackoff (the default), and grows
// by doubling up to MaxFlushBackoff with it; the first success resets it.
func TestManager_FlushPauseGrowsWhileFailing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		maxBackoff        time.Duration
		wantAttempts      int64
		wantAfterRecovery int64
	}{
		{name: "default: fixed pause of one second", maxBackoff: 0, wantAttempts: 60, wantAfterRecovery: 61},
		{name: "growing up to 30 seconds", maxBackoff: 30 * time.Second, wantAttempts: 6, wantAfterRecovery: 7},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			clk := &stepClock{at: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
			sink := &downThenUp{}
			mgr := exporter.NewManager[*exporter.FeatureEvent]([]exporter.Config{{
				Exporter: sink, FlushInterval: time.Hour, MaxEventInMemory: 1,
				MaxFlushBackoff: tt.maxBackoff, Now: clk.now,
			}}, time.Hour, nil)
			t.Cleanup(mgr.Stop)

			for range 60 {
				mgr.AddEvent(&exporter.FeatureEvent{})
				clk.advance(time.Second)
			}

			assert.Equal(t, tt.wantAttempts, sink.calls.Load())

			// the exporter recovers: the next flush that is due succeeds and the pause is back to one second
			sink.recovered.Store(true)
			clk.advance(time.Minute)
			mgr.AddEvent(&exporter.FeatureEvent{})
			assert.Equal(t, tt.wantAfterRecovery, sink.calls.Load())
		})
	}
}

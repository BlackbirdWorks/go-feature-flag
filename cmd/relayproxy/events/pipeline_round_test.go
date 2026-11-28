package events_test

import (
	"context"
	"log/slog"
	"math"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/events"
	"github.com/blackbirdworks/semaphore/exporter"
)

// scripted is an engine exporter that fails the calls whose number is in failOn, and records the keys it exported.
type scripted struct {
	keys   []string
	failOn []int
	calls  int
	mu     sync.Mutex
}

func (*scripted) IsBulk() bool { return true }

func (s *scripted) Export(_ context.Context, _ *slog.Logger, batch []exporter.ExportableEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.calls++

	if slices.Contains(s.failOn, s.calls) {
		return assert.AnError
	}

	for _, event := range batch {
		s.keys = append(s.keys, event.GetKey())
	}

	return nil
}

func (s *scripted) exported() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return slices.Clone(s.keys)
}

// TestDispatcher_NoDuplicateWithTheEngineManager: with the engine manager and a byte limit, a batch that fails does
// not make the batches before it, which succeeded, go out again: every event is exported once or counted once.
func TestDispatcher_NoDuplicateWithTheEngineManager(t *testing.T) {
	t.Parallel()

	size := int64(exporter.EventSize(ev(0)))

	tests := []struct {
		name   string
		failOn []int
		retry  events.RetryPolicy
	}{
		{name: "no failure", retry: events.RetryPolicy{MaxAttempts: 1}},
		{name: "second call fails, kept", failOn: []int{2}, retry: events.RetryPolicy{MaxAttempts: 1}},
		{name: "calls fail, kept", failOn: []int{2, 4, 5}, retry: events.RetryPolicy{MaxAttempts: 1}},
		{
			name: "second call fails, retried", failOn: []int{2},
			retry: events.RetryPolicy{MaxAttempts: 3, InitialBackoff: time.Millisecond, MaxBackoff: time.Millisecond},
		},
		{
			name: "calls fail, dropped", failOn: []int{2, 4}, retry: events.RetryPolicy{Drop: true, MaxAttempts: 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			const count = 12

			sink := &scripted{failOn: tt.failOn}
			disp := newDispatcher(t, &events.DispatcherConfig{After: newTimers(true).after, Rand: halfRand})
			require.NoError(t, disp.Start(context.Background()))

			expDef := events.NewExporterDef("a", kindFile, "v1", sink, exporter.Config{
				MaxBytes: 2 * size, FlushInterval: time.Hour, MaxEventInMemory: 1000,
			})
			expDef.Pipeline = events.Pipeline{MaxBytes: 2 * size, Retry: tt.retry}

			_, err := disp.Apply(context.Background(), []events.ExporterDef{expDef})
			require.NoError(t, err)

			for i := range count {
				require.True(t, disp.Collect(ev(i)))
			}

			_, err = disp.Shutdown(bounded(t))
			require.NoError(t, err)

			got := statusOf(t, new(disp.Status()), "a")
			keys := sink.exported()
			unique := slices.Clone(keys)
			slices.Sort(unique)

			assert.Equal(t, slices.Compact(unique), unique, "no event is exported twice")
			assert.Equal(t, uint64(len(keys)), got.Exported)
			assert.Equal(t, uint64(count), got.Handed)
			assert.Equal(t, got.Handed, got.Exported+got.Dropped.Total(), "exported + dropped by reason = handed")
		})
	}
}

// TestDispatcher_RetryWaitEndsAtTheDrainDeadline: an exporter that is closed while a retry waits (the wait began
// before the close) does not wait the whole backoff, and does not try again after its drain deadline.
func TestDispatcher_RetryWaitEndsAtTheDrainDeadline(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		shutdown bool
	}{
		{name: "reload", shutdown: false},
		{name: "shutdown", shutdown: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tim := newTimers(false)
			disp := newDispatcher(t, &events.DispatcherConfig{After: tim.after, Now: tim.now, Rand: halfRand})
			require.NoError(t, disp.Start(context.Background()))

			// one event reaches the threshold of the engine manager: it flushes, fails, and waits to retry
			sink := &flaky{failFirst: -1}
			expDef := events.NewExporterDef("a", kindFile, "v1", sink, exporter.Config{MaxEventInMemory: 1})
			expDef.Pipeline = events.Pipeline{
				DrainTimeout: time.Nanosecond,
				Retry: events.RetryPolicy{
					MaxAttempts:    5,
					InitialBackoff: 5 * time.Second,
					MaxBackoff:     5 * time.Second,
				},
			}

			_, err := disp.Apply(context.Background(), []events.ExporterDef{expDef})
			require.NoError(t, err)
			require.True(t, disp.Collect(ev(0)))
			await(t, tim.armed, "the retry to wait")

			if tt.shutdown {
				_, err = disp.Shutdown(context.Background())
			} else {
				_, err = disp.Apply(context.Background(), nil)
			}

			require.NoError(t, err)
			require.NoError(t, disp.WaitDrains(bounded(t)), "the exporter finished closing")

			got := statusOf(t, new(disp.Status()), "a")
			assert.Equal(t, uint64(1), got.Dropped.Drain)
			assert.Zero(t, got.Exported)
		})
	}
}

// TestDispatcher_BlockedExporterDoesNotDelayOthers: an exporter with the block policy whose queue is full costs only
// its own events: the other exporter keeps receiving, and the shared channel drops nothing.
func TestDispatcher_BlockedExporterDoesNotDelayOthers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		workers int
	}{
		{name: "one worker", workers: 1},
		{name: "two workers", workers: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			const count = 150

			gate := make(chan struct{})
			managers := map[string]*fakeManager{}

			var mu sync.Mutex

			newManager := func(name string, _ exporter.Config, _ *slog.Logger) events.FeatureManager {
				mgr := &fakeManager{name: name, signal: make(chan struct{}, 1<<10), entered: make(chan struct{}, 1)}
				if name == "x" {
					mgr.gate = gate
				}

				mu.Lock()
				managers[name] = mgr
				mu.Unlock()

				return mgr
			}

			tim := newTimers(false)
			disp := newDispatcher(t, &events.DispatcherConfig{
				Workers: tt.workers, BatchSize: 1, HandoffSize: 64, After: tim.after, NewManager: newManager,
			})
			require.NoError(t, disp.Start(context.Background()))

			blocked := def("x", "v1")
			blocked.Pipeline = events.Pipeline{Overflow: bootstrap.OverflowBlock, BlockTimeout: time.Hour}

			healthy := def("y", "v1")

			_, err := disp.Apply(context.Background(), []events.ExporterDef{blocked, healthy})
			require.NoError(t, err)

			t.Cleanup(func() { safeClose(gate) })

			for i := range count {
				require.True(t, disp.Collect(ev(i)))
			}

			require.Eventually(t, func() bool {
				return statusOf(t, new(disp.Status()), "y").Handed == count
			}, wait, time.Millisecond, "the workers handed every event to the exporter that is not blocked")

			status := disp.Status()
			assert.Zero(t, status.Queue.Dropped.QueueFull, "the shared channel dropped nothing")
			assert.Equal(t, uint64(count), statusOf(t, &status, "x").Handed, "the blocked exporter has its events too")
		})
	}
}

// TestDispatcher_ShutdownWithAnExporterThatWaits: a shutdown whose deadline has passed returns although an exporter
// with the block policy has events waiting for room; they are counted as drained once it finishes.
func TestDispatcher_ShutdownWithAnExporterThatWaits(t *testing.T) {
	t.Parallel()

	fac := newFactory()
	fac.gate = make(chan struct{})
	fac.entered = make(chan struct{}, 1)
	tim := newTimers(false)

	disp := startDispatcher(t, fac, &events.DispatcherConfig{
		Workers: 1, BatchSize: 1, HandoffSize: 2, After: tim.after,
	})

	expDef := def("a", "v1")
	expDef.Pipeline = events.Pipeline{Overflow: bootstrap.OverflowBlock, BlockTimeout: time.Hour}

	_, err := disp.Apply(context.Background(), []events.ExporterDef{expDef})
	require.NoError(t, err)

	require.True(t, disp.Collect(ev(0)))
	await(t, fac.entered, "the manager to hold the first event")

	for i := 1; i < 6; i++ {
		require.True(t, disp.Collect(ev(i)))
	}

	await(t, tim.armed, "the events to wait for room")

	report, err := disp.Shutdown(canceled())
	require.NoError(t, err)
	assert.True(t, report.Drained[0].Cut, "the exporter is held by its manager: the drain is cut")

	close(fac.gate)
	require.NoError(t, disp.WaitDrains(bounded(t)))

	got := statusOf(t, new(disp.Status()), "a")
	assert.Equal(t, got.Handed, got.Exported+got.Dropped.Total(), "exported + dropped by reason = handed")
}

// TestDispatcher_BackoffDoesNotOverflow: a backoff near the limit of the duration is capped before it is doubled.
func TestDispatcher_BackoffDoesNotOverflow(t *testing.T) {
	t.Parallel()

	huge := time.Duration(math.MaxInt64/2 + 1)
	want := time.Duration(0.75 * float64(huge))

	tests := []struct {
		name      string
		wantWaits []time.Duration
		retry     events.RetryPolicy
	}{
		{
			name:      "doubled past the cap",
			retry:     events.RetryPolicy{Drop: true, MaxAttempts: 3, InitialBackoff: huge, MaxBackoff: math.MaxInt64},
			wantWaits: []time.Duration{want, time.Duration(0.75 * float64(math.MaxInt64))},
		},
		{
			name:      "no cap given",
			retry:     events.RetryPolicy{Drop: true, MaxAttempts: 3, InitialBackoff: time.Hour},
			wantWaits: []time.Duration{45 * time.Minute, 45 * time.Minute},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tim := newTimers(true)
			sink := &flaky{failFirst: -1}
			disp := newDispatcher(t, &events.DispatcherConfig{After: tim.after, Now: tim.now, Rand: halfRand})
			require.NoError(t, disp.Start(context.Background()))

			expDef := events.NewExporterDef("a", kindFile, "v1", sink, exporter.Config{MaxEventInMemory: 1})
			expDef.Pipeline = events.Pipeline{Retry: tt.retry}

			_, err := disp.Apply(context.Background(), []events.ExporterDef{expDef})
			require.NoError(t, err)
			require.True(t, disp.Collect(ev(0)))

			_, err = disp.Shutdown(bounded(t))
			require.NoError(t, err)

			assert.Equal(t, tt.wantWaits, tim.recorded()[:2])
		})
	}
}

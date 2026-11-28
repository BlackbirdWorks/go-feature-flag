package events_test

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/events"
	"github.com/blackbirdworks/semaphore/exporter"
)

// timers is the injected timer of a dispatcher: it records the waits it was asked for and answers with a channel
// that the test controls (fire), and it tells when a wait started (armed).
type timers struct {
	fire  chan time.Time
	armed chan struct{}
	waits []time.Duration
	mu    sync.Mutex
}

func newTimers(fired bool) *timers {
	tim := &timers{fire: make(chan time.Time), armed: make(chan struct{}, 1<<10)}
	if fired {
		close(tim.fire)
	}

	return tim
}

func (t *timers) after(wait time.Duration) <-chan time.Time {
	t.mu.Lock()
	t.waits = append(t.waits, wait)
	t.mu.Unlock()

	select {
	case t.armed <- struct{}{}:
	default:
	}

	return t.fire
}

func (t *timers) recorded() []time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()

	return append([]time.Duration(nil), t.waits...)
}

// halfRand is the source of jitter of the tests: the wait is three quarters of the backoff.
func halfRand() float64 { return 0.5 }

// flaky is an engine exporter that fails its first failFirst calls (all of them when failFirst is negative).
type flaky struct {
	batches   [][]string
	failFirst int
	calls     atomic.Int64
	mu        sync.Mutex
}

func (*flaky) IsBulk() bool { return true }

func (f *flaky) Export(_ context.Context, _ *slog.Logger, batch []exporter.ExportableEvent) error {
	call := int(f.calls.Add(1))

	if f.failFirst < 0 || call <= f.failFirst {
		return assert.AnError
	}

	keys := make([]string, 0, len(batch))
	for _, event := range batch {
		keys = append(keys, event.GetKey())
	}

	f.mu.Lock()
	f.batches = append(f.batches, keys)
	f.mu.Unlock()

	return nil
}

func (f *flaky) sizes() []int {
	f.mu.Lock()
	defer f.mu.Unlock()

	if len(f.batches) == 0 {
		return nil
	}

	out := make([]int, 0, len(f.batches))

	for _, batch := range f.batches {
		out = append(out, len(batch))
	}

	return out
}

// TestDispatcher_OverflowPolicy: a full queue of an exporter does what its policy says. The manager is held by a
// gate, so the queue (two events) fills: one event is in the manager, two in the queue, three more arrive.
func TestDispatcher_OverflowPolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		policy       string
		wantKeys     []string
		wantOverflow uint64
		wantBlocked  uint64
		// wantHanded is what the worker has offered when it waits: a blocked sender holds the batches behind it.
		wantHanded uint64
		fired      bool
	}{
		{
			name: "dropNewest", policy: bootstrap.OverflowDropNewest,
			wantKeys: []string{keyFlag0, eventOneKey, eventTwoKey}, wantOverflow: 3, wantHanded: 6,
		},
		{
			name:         "default",
			policy:       "",
			wantKeys:     []string{keyFlag0, eventOneKey, eventTwoKey},
			wantOverflow: 3,
			wantHanded:   6,
		},
		{
			name: "dropOldest", policy: bootstrap.OverflowDropOldest,
			wantKeys: []string{keyFlag0, keyFlag4, "flag-5"}, wantOverflow: 3, wantHanded: 6,
		},
		{
			name: "block deadline", policy: bootstrap.OverflowBlock, fired: true,
			wantKeys: []string{keyFlag0, eventOneKey, eventTwoKey}, wantBlocked: 3, wantHanded: 6,
		},
		{
			name: "block with room", policy: bootstrap.OverflowBlock,
			wantKeys: []string{keyFlag0, eventOneKey, eventTwoKey, "flag-3", keyFlag4, "flag-5"}, wantHanded: 4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fac := newFactory()
			fac.gate = make(chan struct{})
			fac.entered = make(chan struct{}, 1)
			tim := newTimers(tt.fired)

			disp := startDispatcher(t, fac, &events.DispatcherConfig{
				Workers: 1, BatchSize: 1, HandoffSize: 2, After: tim.after,
			})

			expDef := def("a", "v1")
			expDef.Pipeline = events.Pipeline{Overflow: tt.policy, BlockTimeout: time.Second}

			_, err := disp.Apply(context.Background(), []events.ExporterDef{expDef})
			require.NoError(t, err)

			// the manager holds the first event; the next ones find the queue of the exporter
			require.True(t, disp.Collect(ev(0)))
			await(t, fac.entered, "the manager to hold the first event")

			for i := 1; i < 6; i++ {
				require.True(t, disp.Collect(ev(i)))
			}

			if tt.policy == bootstrap.OverflowBlock && !tt.fired {
				await(t, tim.armed, "a sender to wait for room")
			}

			require.Eventually(t, func() bool {
				got := statusOf(t, new(disp.Status()), "a")

				return got.Handed == tt.wantHanded &&
					got.Dropped.Overflow+got.Dropped.BlockTimeout == tt.wantOverflow+tt.wantBlocked
			}, wait, time.Millisecond, "the drops of the policy were counted")

			close(fac.gate)
			fac.of("a", 0).waitFor(t, len(tt.wantKeys))

			_, err = disp.Shutdown(bounded(t))
			require.NoError(t, err)

			got := statusOf(t, new(disp.Status()), "a")
			assert.Zero(t, got.Dropped.Drain, "every drop has its own reason")
			assert.Equal(t, uint64(len(tt.wantKeys)), got.Exported)
			assert.ElementsMatch(t, tt.wantKeys, keys(fac.of("a", 0).got()))
			assert.Equal(t, tt.wantOverflow, got.Dropped.Overflow)
			assert.Equal(t, tt.wantBlocked, got.Dropped.BlockTimeout)

			wantPolicy := tt.policy
			if wantPolicy == "" {
				wantPolicy = bootstrap.OverflowDropNewest
			}

			assert.Equal(t, wantPolicy, got.OverflowPolicy)
		})
	}
}

// TestDispatcher_Batches: the exporter gets batches of at most maxBytes (an event larger than that goes alone), and a
// failed export is retried with exponential backoff and jitter until the attempts or the time are spent; a batch
// that is spent is dropped and counted.
func TestDispatcher_Batches(t *testing.T) {
	t.Parallel()

	size := int64(exporter.EventSize(ev(0)))
	retry := events.RetryPolicy{
		MaxAttempts: 4, InitialBackoff: 100 * time.Millisecond, MaxBackoff: 150 * time.Millisecond,
	}

	tests := []struct {
		name          string
		wantSizes     []int
		wantWaits     []time.Duration
		pipe          events.Pipeline
		failFirst     int
		wantCalls     int64
		wantExported  uint64
		wantExhausted uint64
		wantDrain     uint64
	}{
		{
			name: "no limit", pipe: events.Pipeline{}, wantSizes: []int{5}, wantCalls: 1, wantExported: 5,
		},
		{
			name: "two events", pipe: events.Pipeline{MaxBytes: 2 * size}, wantSizes: []int{2, 2, 1}, wantCalls: 3,
			wantExported: 5,
		},
		{
			name: "event larger than the limit", pipe: events.Pipeline{MaxBytes: size - 1},
			wantSizes: []int{1, 1, 1, 1, 1}, wantCalls: 5, wantExported: 5,
		},
		{
			name: "one retry", pipe: events.Pipeline{Retry: retry}, failFirst: 1, wantSizes: []int{5}, wantCalls: 2,
			wantWaits: []time.Duration{75 * time.Millisecond}, wantExported: 5,
		},
		{
			name: "backoff doubles and is capped", pipe: events.Pipeline{Retry: retry}, failFirst: 3,
			wantSizes: []int{5}, wantCalls: 4,
			wantWaits:    []time.Duration{75 * time.Millisecond, 112500 * time.Microsecond, 112500 * time.Microsecond},
			wantExported: 5,
		},
		{
			name: "attempts spent", pipe: events.Pipeline{Retry: retry}, failFirst: -1, wantCalls: 4,
			wantWaits:     []time.Duration{75 * time.Millisecond, 112500 * time.Microsecond, 112500 * time.Microsecond},
			wantExhausted: 5,
		},
		{
			name: "time spent", wantCalls: 2, failFirst: -1, wantWaits: []time.Duration{75 * time.Millisecond},
			pipe: events.Pipeline{Retry: events.RetryPolicy{
				MaxAttempts: 10, InitialBackoff: 100 * time.Millisecond, MaxBackoff: time.Second,
				MaxElapsed: 200 * time.Millisecond,
			}},
			wantExhausted: 5,
		},
		{
			name: "one attempt", pipe: events.Pipeline{Retry: events.RetryPolicy{MaxAttempts: 1}}, failFirst: -1,
			wantCalls: 1, wantExhausted: 5,
		},
		{
			name: "retry off", pipe: events.Pipeline{}, failFirst: -1, wantCalls: 1, wantDrain: 5,
		},
		{
			name: "spent batch of a split", failFirst: -1, wantCalls: 3, wantExhausted: 5,
			pipe: events.Pipeline{MaxBytes: 2 * size, Retry: events.RetryPolicy{MaxAttempts: 1}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tim := newTimers(true)
			sink := &flaky{failFirst: tt.failFirst}
			disp := startDispatcher(t, newFactory(), &events.DispatcherConfig{After: tim.after, Rand: halfRand})

			expDef := events.NewExporterDef("a", kindFile, "v1", sink, exporter.Config{MaxBytes: tt.pipe.MaxBytes})
			expDef.Pipeline = tt.pipe

			_, err := disp.Apply(context.Background(), []events.ExporterDef{expDef})
			require.NoError(t, err)

			for i := range 5 {
				require.True(t, disp.Collect(ev(i)))
			}

			_, err = disp.Shutdown(bounded(t))
			require.NoError(t, err)

			got := statusOf(t, new(disp.Status()), "a")
			assert.Equal(t, tt.wantCalls, sink.calls.Load())
			assert.Equal(t, tt.wantWaits, tim.recorded())
			assert.Equal(t, tt.wantSizes, sink.sizes())
			assert.Equal(t, tt.wantExported, got.Exported)
			assert.Equal(t, tt.wantExhausted, got.Dropped.RetryExhausted)
			assert.Equal(t, tt.wantDrain, got.Dropped.Drain)
			assert.Equal(t, uint64(5), got.Handed)
		})
	}
}

// TestDispatcher_Drain: the drain of an exporter ends at its own deadline, whichever of a reload and a shutdown
// closes it. An exporter that is cut keeps closing and delivers what it holds when it can.
func TestDispatcher_Drain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		drain time.Duration
		// dispatchDrain is the drain timeout of the dispatcher: the one of the exporter wins, longer or shorter.
		dispatchDrain time.Duration
		wantDrained   uint64
		wantExported  uint64
		shutdown      bool
		held          bool
		wantCut       bool
	}{
		{name: "reload deadline passed", held: true, drain: time.Nanosecond, dispatchDrain: time.Hour, wantCut: true,
			wantExported: 3},
		{name: "reload within the deadline", drain: time.Hour, dispatchDrain: time.Nanosecond, wantExported: 3},
		{
			name: "shutdown deadline passed", shutdown: true, held: true, drain: time.Nanosecond,
			dispatchDrain: time.Hour, wantCut: true, wantExported: 3,
		},
		{
			name: "shutdown within the deadline", shutdown: true, drain: time.Hour, dispatchDrain: time.Nanosecond,
			wantExported: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gate := make(chan struct{})

			entered := make(chan struct{})

			exp := &gatedExporter{gate: gate, entered: entered}

			fac := newFactory()
			disp := startDispatcher(t, fac, &events.DispatcherConfig{
				After: newTimers(false).after, Rand: halfRand, DrainTimeout: tt.dispatchDrain,
			})

			expDef := events.NewExporterDef("a", kindFile, "v1", exp, exporter.Config{})
			expDef.Pipeline = events.Pipeline{
				DrainTimeout: tt.drain,
				Retry:        events.RetryPolicy{MaxAttempts: 5, InitialBackoff: time.Second, MaxBackoff: time.Second},
			}

			_, err := disp.Apply(context.Background(), []events.ExporterDef{expDef})
			require.NoError(t, err)

			for i := range 3 {
				require.True(t, disp.Collect(ev(i)))
			}

			fac.of("a", 0).waitFor(t, 3)

			closed := make(chan bool, 1)

			go func() {
				if tt.shutdown {
					report, shutdownErr := disp.Shutdown(context.Background())
					assert.NoError(t, shutdownErr)

					closed <- report.Drained[0].Cut

					return
				}

				set, applyErr := disp.Apply(context.Background(), nil)
				assert.NoError(t, applyErr)

				closed <- len(set.Cut) == 1
			}()

			// an exporter that is not held is released while its drain runs, which a shorter deadline would cut
			if !tt.held {
				await(t, entered, "the exporter to start its last flush")
				safeClose(gate)
			}

			cut := await(t, closed, "the drain to end")

			assert.Equal(t, tt.wantCut, cut)

			safeClose(gate)
			require.NoError(t, disp.WaitDrains(bounded(t)))

			got := statusOf(t, new(disp.Status()), "a")
			assert.Equal(t, tt.wantDrained, got.Dropped.Drain)
			assert.Equal(t, tt.wantExported, got.Exported)
		})
	}
}

// TestDispatcher_RetryEndsAtTheDrainDeadline: a retry that the drain deadline ends gives its events up as drained
// instead of waiting for the next attempt.
func TestDispatcher_RetryEndsAtTheDrainDeadline(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		shutdown    bool
		wantDrained uint64
	}{
		{name: "shutdown", shutdown: true, wantDrained: 3},
		{name: "reload", wantDrained: 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fac := newFactory()
			disp := startDispatcher(t, fac, &events.DispatcherConfig{After: newTimers(false).after, Rand: halfRand})

			expDef := events.NewExporterDef("a", kindFile, "v1", &flaky{failFirst: -1}, exporter.Config{})
			expDef.Pipeline = events.Pipeline{
				DrainTimeout: time.Nanosecond,
				Retry:        events.RetryPolicy{MaxAttempts: 5, InitialBackoff: time.Second, MaxBackoff: time.Second},
			}

			_, err := disp.Apply(context.Background(), []events.ExporterDef{expDef})
			require.NoError(t, err)

			for i := range 3 {
				require.True(t, disp.Collect(ev(i)))
			}

			fac.of("a", 0).waitFor(t, 3)

			if tt.shutdown {
				_, err = disp.Shutdown(context.Background())
			} else {
				_, err = disp.Apply(context.Background(), nil)
			}

			require.NoError(t, err)
			require.NoError(t, disp.WaitDrains(bounded(t)))

			got := statusOf(t, new(disp.Status()), "a")
			assert.Equal(t, tt.wantDrained, got.Dropped.Drain)
			assert.Zero(t, got.Exported)
			assert.Zero(t, got.Dropped.RetryExhausted)
		})
	}
}

// TestDispatcher_RetryWhileRunning: a batch that fails while the exporter runs is retried by the pipeline, and the
// events arrive once the export succeeds; no drop is counted.
func TestDispatcher_RetryWhileRunning(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		failFirst int
		wantCalls int64
		wantWaits int
	}{
		{name: "first attempt", failFirst: 0, wantCalls: 1, wantWaits: 0},
		{name: "second attempt", failFirst: 1, wantCalls: 2, wantWaits: 1},
		{name: "third attempt", failFirst: 2, wantCalls: 3, wantWaits: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tim := newTimers(true)
			sink := &flaky{failFirst: tt.failFirst}
			disp := newDispatcher(t, &events.DispatcherConfig{After: tim.after, Rand: halfRand})
			require.NoError(t, disp.Start(context.Background()))

			// one event reaches the threshold of the manager: it flushes while the exporter runs
			expDef := events.NewExporterDef("a", kindFile, "v1", sink, exporter.Config{MaxEventInMemory: 1})
			expDef.Pipeline = events.Pipeline{
				Retry: events.RetryPolicy{
					MaxAttempts:    5,
					InitialBackoff: time.Millisecond,
					MaxBackoff:     time.Millisecond,
				},
			}

			_, err := disp.Apply(context.Background(), []events.ExporterDef{expDef})
			require.NoError(t, err)
			require.True(t, disp.Collect(ev(0)))

			require.Eventually(t, func() bool { return len(sink.sizes()) == 1 }, wait, time.Millisecond)

			assert.Equal(t, tt.wantCalls, sink.calls.Load())
			assert.Len(t, tim.recorded(), tt.wantWaits)
			assert.Zero(t, statusOf(t, new(disp.Status()), "a").Dropped.Total())
		})
	}
}

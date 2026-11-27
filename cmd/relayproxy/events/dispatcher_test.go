package events_test

import (
	"context"
	"log/slog"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/events"
	"github.com/blackbirdworks/semaphore/exporter"
)

// TestDispatcher_SharedManagers checks that M exporters shared by N flag sets are M managers, not M x N.
func TestDispatcher_SharedManagers(t *testing.T) {
	t.Parallel()

	for _, flagSets := range []int{1, 100, 5000} {
		t.Run(strconv.Itoa(flagSets)+" flag sets", func(t *testing.T) {
			t.Parallel()

			fac := newFactory()
			disp := startDispatcher(t, fac, &events.DispatcherConfig{QueueSize: flagSets + 1})

			_, err := disp.Apply(context.Background(), defs("a", "b", "c"))
			require.NoError(t, err)

			// each flag set evaluates one flag: its event goes to the one channel
			for i := range flagSets {
				assert.True(t, disp.Collect(ev(i)))
			}

			_, err = disp.Shutdown(bounded(t))
			require.NoError(t, err)

			assert.Equal(t, 3, fac.count(), "managers built")

			for _, name := range []string{"a", "b", "c"} {
				assert.Equal(t, eventKeys(flagSets), keys(fac.of(name, 0).got()), "events of %s", name)
			}
		})
	}
}

// TestDispatcher_GoroutinesDoNotGrowWithFlagSets counts the goroutines the dispatcher owns (those started under
// a label) after the events of N flag sets went through it: the workers and one goroutine per exporter, whatever N.
func TestDispatcher_GoroutinesDoNotGrowWithFlagSets(t *testing.T) {
	t.Parallel()

	const (
		workers   = 3
		exporters = 4
	)

	for _, flagSets := range []int{1, 100, 5000} {
		t.Run(strconv.Itoa(flagSets), func(t *testing.T) {
			t.Parallel()

			disp := newDispatcher(t, &events.DispatcherConfig{
				NewManager: newFactory().build, Workers: workers, QueueSize: 16,
			})

			label := labeledRun(t, func(ctx context.Context) {
				require.NoError(t, disp.Start(ctx))

				_, err := disp.Apply(ctx, defs("a", "b", "c", "d"))
				require.NoError(t, err)
			})

			for i := range flagSets {
				disp.Collect(ev(i))
			}

			assert.Equal(t, workers+exporters, labelled(label), "goroutines with %d flag sets", flagSets)

			_, err := disp.Shutdown(bounded(t))
			require.NoError(t, err)
			assertNoLabelled(t, label, "goroutines after the shutdown")
		})
	}
}

// TestDispatcher_ExactlyOnce collects events from many goroutines and checks that every exporter got every
// accepted event once.
func TestDispatcher_ExactlyOnce(t *testing.T) {
	t.Parallel()

	const (
		collectors = 16
		perCollect = 500
	)

	fac := newFactory()
	disp := startDispatcher(t, fac, &events.DispatcherConfig{Workers: 4, QueueSize: collectors * perCollect})

	_, err := disp.Apply(context.Background(), defs("a", "b", "c"))
	require.NoError(t, err)

	var wg sync.WaitGroup

	for c := range collectors {
		wg.Go(func() {
			for i := range perCollect {
				assert.True(t, disp.Collect(ev(c*perCollect+i)))
			}
		})
	}

	wg.Wait()

	report, err := disp.Shutdown(bounded(t))
	require.NoError(t, err)

	all := make([]*exporter.FeatureEvent, 0, collectors*perCollect)
	for i := range collectors * perCollect {
		all = append(all, ev(i))
	}

	wantSorted := keys(all)

	for _, name := range []string{"a", "b", "c"} {
		assert.Equal(t, wantSorted, keys(fac.of(name, 0).got()), "events of %s", name)
	}

	assert.Equal(t, events.Drops{}, report.Dropped)
	assert.Equal(t, uint64(collectors*perCollect), disp.Status().Queue.Accepted)
}

// TestDispatcher_Overflow fills the shared channel before the workers run: exactly the capacity is queued, the
// rest is dropped and counted, and no Collect waits.
func TestDispatcher_Overflow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		capacity        int
		sent            int
		wantAccepted    uint64
		wantQueueFull   uint64
		wantDeliveredTo int
	}{
		{name: "one over", capacity: 4, sent: 5, wantAccepted: 4, wantQueueFull: 1, wantDeliveredTo: 4},
		{name: "many over", capacity: 8, sent: 108, wantAccepted: 8, wantQueueFull: 100, wantDeliveredTo: 8},
		{name: "exactly full", capacity: 5, sent: 5, wantAccepted: 5, wantQueueFull: 0, wantDeliveredTo: 5},
		{name: "under", capacity: 5, sent: 2, wantAccepted: 2, wantQueueFull: 0, wantDeliveredTo: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fac := newFactory()
			disp := newDispatcher(t, &events.DispatcherConfig{NewManager: fac.build, QueueSize: tt.capacity})

			_, err := disp.Apply(context.Background(), defs("a"))
			require.NoError(t, err)

			// no worker runs yet: nothing takes the events out of the channel
			finished := make(chan int, 1)

			go func() {
				accepted := 0

				for i := range tt.sent {
					if disp.Collect(ev(i)) {
						accepted++
					}
				}

				finished <- accepted
			}()

			// every Collect returns although nothing is taking events: none waited
			assert.Equal(t, int(tt.wantAccepted), await(t, finished, "every Collect to return"))

			status := disp.Status()
			assert.Equal(t, int(tt.wantAccepted), status.Queue.Length)
			assert.Equal(t, tt.capacity, status.Queue.Capacity)
			assert.Equal(t, tt.wantAccepted, status.Queue.Accepted)
			assert.Equal(t, tt.wantQueueFull, status.Queue.Dropped.QueueFull)

			require.NoError(t, disp.Start(context.Background()))

			report, err := disp.Shutdown(bounded(t))
			require.NoError(t, err)
			assert.Len(
				t,
				fac.of("a", 0).got(),
				tt.wantDeliveredTo,
				"the queued events are delivered, the dropped are not",
			)
			assert.Equal(t, tt.wantQueueFull, report.Dropped.QueueFull)

			final := disp.Status()
			assertGlobalAccounting(t, &final, uint64(tt.wantDeliveredTo), "after the shutdown")
			assertExporterAccounting(t, &final, "after the shutdown")
		})
	}
}

// TestDispatcher_SlowExporterDelaysNobody gates the manager of one exporter. The workers never wait on it: the
// other exporter receives every event, the hand-off of the slow one holds its capacity and it drops, and counts,
// the rest against itself.
func TestDispatcher_SlowExporterDelaysNobody(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		wantSlowKeys []string
		handoff      int
		sent         int
		wantOverflow uint64
		wantQueued   int64
	}{
		// the manager holds the first event (BatchSize 1) and the hand-off the next ones
		{
			name:         "burst far above the hand-off",
			handoff:      8,
			sent:         101,
			wantOverflow: 92,
			wantQueued:   9,
			wantSlowKeys: eventKeys(9),
		},
		{
			name:         "burst within the hand-off",
			handoff:      200,
			sent:         101,
			wantOverflow: 0,
			wantQueued:   101,
			wantSlowKeys: eventKeys(101),
		},
		{name: "one event over", handoff: 8, sent: 10, wantOverflow: 1, wantQueued: 9, wantSlowKeys: eventKeys(9)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			slow, fast := newFactory(), newFactory()
			slow.gate = newGate(t)
			slow.entered = make(chan struct{}, 1)

			// one dispatcher, two exporters: the factory gives the gated manager to "a-slow" only
			build := func(name string, cfg exporter.Config, logger *slog.Logger) events.FeatureManager {
				if name == "a-slow" {
					return slow.build(name, cfg, logger)
				}

				return fast.build(name, cfg, logger)
			}

			disp := newDispatcher(t, &events.DispatcherConfig{
				NewManager: build, HandoffSize: tt.handoff, BatchSize: 1, Workers: 2,
			})
			require.NoError(t, disp.Start(context.Background()))

			_, err := disp.Apply(context.Background(), []events.ExporterDef{def("a-slow", "v1"), def("b-fast", "v1")})
			require.NoError(t, err)

			require.True(t, disp.Collect(ev(0)))
			await(t, slow.entered, "the slow manager to take its first event")
			fast.of("b-fast", 0).waitFor(t, 1)

			// one event at a time, so that the fast exporter keeps up whatever the size of its hand-off: the
			// workers offer to "a-slow" before "b-fast", so when the fast one has an event the slow one was offered it
			for i := 1; i < tt.sent; i++ {
				require.True(t, disp.Collect(ev(i)), "the shared channel keeps flowing")
				fast.of("b-fast", 0).waitFor(t, 1)
			}

			status := disp.Status()
			slowStatus, fastStatus := statusOf(t, &status, "a-slow"), statusOf(t, &status, "b-fast")

			assert.Equal(t, uint64(tt.sent), fastStatus.Handed)
			assert.Zero(t, fastStatus.Dropped.Overflow, "the fast exporter drops nothing")
			assert.Equal(t, uint64(tt.sent), slowStatus.Handed)
			assert.Equal(t, tt.wantOverflow, slowStatus.Dropped.Overflow, "the drops are the slow exporter's")
			assert.Equal(t, tt.wantQueued, slowStatus.Queued)
			assert.Zero(t, status.Queue.Dropped.QueueFull, "nothing was dropped from the shared channel")
			assertExporterAccounting(t, &status, "while the slow exporter is gated")

			close(slow.gate)

			_, err = disp.Shutdown(bounded(t))
			require.NoError(t, err)

			assert.Equal(t, eventKeys(tt.sent), keys(fast.of("b-fast", 0).got()))
			assert.Equal(t, tt.wantSlowKeys, keys(slow.of("a-slow", 0).got()), "the events the slow exporter kept")

			final := disp.Status()
			assertExporterAccounting(t, &final, "after the shutdown")
			assertGlobalAccounting(t, &final, uint64(tt.sent), "after the shutdown")
		})
	}
}

// TestDispatcher_SlowExporterWithTheEngineManager is the same property with the real engine manager, whose
// store waits up to 200 ms for room: a burst of 300k events at full speed. The exporter that blocks drops against
// itself; the other one receives every event the shared channel accepted.
func TestDispatcher_SlowExporterWithTheEngineManager(t *testing.T) {
	t.Parallel()

	const burst = 300_000

	gate := newGate(t)
	slow := &gatedExporter{gate: gate}
	fast := &nopExporter{}

	disp := newDispatcher(t, &events.DispatcherConfig{HandoffSize: burst, QueueSize: 1 << 16})
	require.NoError(t, disp.Start(context.Background()))

	_, err := disp.Apply(context.Background(), []events.ExporterDef{
		// the engine store of the slow exporter holds 1000 events: the rest overflows
		events.NewExporterDef("a-slow", "file", "v1", slow, exporter.Config{
			FlushInterval: time.Hour, MaxEventInMemory: 100, MaxQueuedEvents: 1000,
		}),
		events.NewExporterDef("b-fast", "file", "v1", fast, exporter.Config{
			FlushInterval: time.Hour, MaxEventInMemory: 1000, MaxQueuedEvents: burst,
		}),
	})
	require.NoError(t, err)

	accepted := 0

	for i := range burst {
		if disp.Collect(ev(i)) {
			accepted++
		}
	}

	close(gate)

	report, err := disp.Shutdown(bounded(t))
	require.NoError(t, err)

	status := disp.Status()
	slowStatus, fastStatus := statusOf(t, &status, "a-slow"), statusOf(t, &status, "b-fast")

	assert.Equal(t, uint64(accepted), status.Queue.Accepted)
	assert.Equal(t, uint64(burst-accepted), report.Dropped.QueueFull)
	assert.Equal(t, uint64(accepted), fastStatus.Exported, "the fast exporter got every accepted event")
	assert.Zero(t, fastStatus.Dropped.Overflow+fastStatus.Dropped.Drain)
	assert.Positive(t, slowStatus.Dropped.Overflow, "the slow exporter dropped against itself")
	assert.Equal(t, uint64(accepted), slowStatus.Handed)
	assertExporterAccounting(t, &status, "after the burst")
	assertGlobalAccounting(t, &status, uint64(accepted), "after the burst")
}

//nolint:paralleltest // testing.AllocsPerRun panics in a parallel test
func TestDispatcher_NoExporterCostsNothing(t *testing.T) {
	disp := startDispatcher(t, newFactory(), &events.DispatcherConfig{})
	event := ev(1)

	assert.False(t, disp.Collect(event))
	assert.Zero(t, testing.AllocsPerRun(100, func() { disp.Collect(event) }), "allocations of Collect")

	status := disp.Status()
	assert.Equal(t, events.QueueStatus{Capacity: events.DefaultQueueSize}, status.Queue, "nothing counted")
}

//nolint:paralleltest // testing.AllocsPerRun panics in a parallel test
func TestDispatcher_CollectAllocatesNothing(t *testing.T) {
	fac := newFactory()
	fac.gate = newGate(t)

	disp := startDispatcher(t, fac, &events.DispatcherConfig{QueueSize: 1 << 16})

	_, err := disp.Apply(context.Background(), defs("a"))
	require.NoError(t, err)

	event := ev(1)

	assert.Zero(t, testing.AllocsPerRun(1000, func() { disp.Collect(event) }), "allocations of Collect")
	close(fac.gate)
}

func TestDispatcher_LifecycleErrors(t *testing.T) {
	t.Parallel()

	start := func(t *testing.T, disp *events.Dispatcher) {
		t.Helper()
		require.NoError(t, disp.Start(context.Background()))
	}

	shutdown := func(t *testing.T, disp *events.Dispatcher) {
		t.Helper()
		start(t, disp)

		_, err := disp.Shutdown(bounded(t))
		require.NoError(t, err)
	}

	tests := []struct {
		prepare func(t *testing.T, disp *events.Dispatcher)
		act     func(disp *events.Dispatcher) error
		wantErr error
		name    string
	}{
		{
			name: "a second Start", prepare: start, wantErr: events.ErrAlreadyStarted,
			act: func(disp *events.Dispatcher) error { return disp.Start(context.Background()) },
		},
		{
			name: "Start after the shutdown", prepare: shutdown, wantErr: events.ErrClosed,
			act: func(disp *events.Dispatcher) error { return disp.Start(context.Background()) },
		},
		{
			name: "Apply after the shutdown", prepare: shutdown, wantErr: events.ErrClosed,
			act: func(disp *events.Dispatcher) error {
				_, err := disp.Apply(context.Background(), defs("a"))

				return err
			},
		},
		{
			name: "Reload after the shutdown", prepare: shutdown, wantErr: events.ErrClosed,
			act: func(disp *events.Dispatcher) error {
				_, err := disp.Reload(context.Background(), events.Specs{})

				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			disp := newDispatcher(t, &events.DispatcherConfig{NewManager: newFactory().build})
			tt.prepare(t, disp)

			require.ErrorIs(t, tt.act(disp), tt.wantErr)
		})
	}
}

func TestDispatcher_ContextEndShutsDown(t *testing.T) {
	t.Parallel()

	fac := newFactory()
	disp := newDispatcher(t, &events.DispatcherConfig{NewManager: fac.build})
	ctx, cancel := context.WithCancel(context.Background())

	require.NoError(t, disp.Start(ctx))

	_, err := disp.Apply(context.Background(), defs("a"))
	require.NoError(t, err)
	require.True(t, disp.Collect(ev(1)))

	cancel()
	await(t, disp.Done(), "the shutdown that the end of the context starts")

	assert.Equal(t, 1, fac.of("a", 0).stopCount())
	assert.Equal(t, []string{eventOneKey}, keys(fac.of("a", 0).got()))
	assert.False(t, disp.Collect(ev(2)))
	assert.Equal(t, uint64(1), disp.Status().Queue.Dropped.Shutdown)
}

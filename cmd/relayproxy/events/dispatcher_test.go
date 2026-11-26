package events_test

import (
	"context"
	"strconv"
	"sync"
	"testing"

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
				assert.Len(t, fac.of(name, 0).got(), flagSets, "events of %s", name)
			}
		})
	}
}

// TestDispatcher_GoroutinesDoNotGrowWithFlagSets counts the goroutines the dispatcher owns (those started under
// a label) after the events of N flag sets went through it: the count is the same for every N.
func TestDispatcher_GoroutinesDoNotGrowWithFlagSets(t *testing.T) {
	t.Parallel()

	const (
		workers   = 3
		exporters = 4
	)

	// the workers and nothing else: the fake managers own no goroutine
	want := workers

	for _, flagSets := range []int{1, 100, 5000} {
		t.Run(strconv.Itoa(flagSets), func(t *testing.T) {
			t.Parallel()

			var disp *events.Dispatcher

			label := labeledRun(t, func(ctx context.Context) {
				disp = events.NewDispatcher(&events.DispatcherConfig{
					NewManager: newFactory().build, Workers: workers, QueueSize: 16,
				})

				require.NoError(t, disp.Start(ctx))
			})

			_, err := disp.Apply(context.Background(), defs("a", "b", "c", "d")[:exporters])
			require.NoError(t, err)

			for i := range flagSets {
				disp.Collect(ev(i))
			}

			assert.Equal(t, want, labelled(label), "goroutines with %d flag sets", flagSets)

			_, err = disp.Shutdown(bounded(t))
			require.NoError(t, err)
			assert.Equal(t, 0, labelled(label), "goroutines after the shutdown")
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

// TestDispatcher_Overflow gates the only worker and checks that exactly the capacity is queued, the rest is
// dropped and counted, and that no Collect waited.
func TestDispatcher_Overflow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		capacity    int
		extra       int
		wantQueued  int
		wantDropped uint64
	}{
		{name: "one over", capacity: 4, extra: 1, wantQueued: 4, wantDropped: 1},
		{name: "many over", capacity: 8, extra: 100, wantQueued: 8, wantDropped: 100},
		{name: "exactly full", capacity: 5, extra: 0, wantQueued: 5, wantDropped: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fac := newFactory()
			fac.gate = make(chan struct{})
			fac.entered = make(chan struct{}, 1)

			disp := startDispatcher(t, fac, &events.DispatcherConfig{Workers: 1, QueueSize: tt.capacity, BatchSize: 1})

			_, err := disp.Apply(context.Background(), defs("a"))
			require.NoError(t, err)

			// the first event is taken by the worker, which then blocks in the manager: the channel is empty
			require.True(t, disp.Collect(ev(-1)))
			await(t, fac.entered, "the worker to take the first event")

			accepted := 0
			finished := make(chan struct{})

			go func() {
				defer close(finished)

				for i := range tt.capacity + tt.extra {
					if disp.Collect(ev(i)) {
						accepted++
					}
				}
			}()

			// every Collect returns while the worker is blocked: none waited
			await(t, finished, "every Collect to return")

			status := disp.Status()

			assert.Equal(t, tt.wantQueued, accepted)
			assert.Equal(t, tt.wantQueued, status.Queue.Length)
			assert.Equal(t, tt.capacity, status.Queue.Capacity)
			assert.Equal(t, tt.wantDropped, status.Queue.Dropped.QueueFull)

			close(fac.gate)

			report, err := disp.Shutdown(bounded(t))
			require.NoError(t, err)
			assert.Len(t, fac.of("a", 0).got(), 1+tt.wantQueued, "the queued events are delivered, the dropped are not")
			assert.Equal(t, tt.wantDropped, report.Dropped.QueueFull)
		})
	}
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
	fac.gate = make(chan struct{})

	disp := startDispatcher(t, fac, &events.DispatcherConfig{QueueSize: 1 << 16})

	_, err := disp.Apply(context.Background(), defs("a"))
	require.NoError(t, err)

	event := ev(1)

	assert.Zero(t, testing.AllocsPerRun(1000, func() { disp.Collect(event) }), "allocations of Collect")
	close(fac.gate)
}

func TestDispatcher_StartErrors(t *testing.T) {
	t.Parallel()

	disp := events.NewDispatcher(&events.DispatcherConfig{NewManager: newFactory().build})

	require.NoError(t, disp.Start(context.Background()))
	require.ErrorIs(t, disp.Start(context.Background()), events.ErrAlreadyStarted)

	_, err := disp.Shutdown(bounded(t))
	require.NoError(t, err)

	require.ErrorIs(t, disp.Start(context.Background()), events.ErrClosed)

	_, err = disp.Apply(context.Background(), defs("a"))
	require.ErrorIs(t, err, events.ErrClosed)

	_, err = disp.Reload(context.Background(), events.Specs{})
	require.ErrorIs(t, err, events.ErrClosed)
}

func TestDispatcher_ContextEndShutsDown(t *testing.T) {
	t.Parallel()

	fac := newFactory()
	disp := events.NewDispatcher(&events.DispatcherConfig{NewManager: fac.build})
	ctx, cancel := context.WithCancel(context.Background())

	require.NoError(t, disp.Start(ctx))

	_, err := disp.Apply(context.Background(), defs("a"))
	require.NoError(t, err)
	require.True(t, disp.Collect(ev(1)))

	cancel()
	await(t, disp.Done(), "the shutdown that the end of the context starts")

	assert.Equal(t, 1, fac.of("a", 0).stopCount())
	assert.Len(t, fac.of("a", 0).got(), 1)
	assert.False(t, disp.Collect(ev(2)))
	assert.Equal(t, uint64(1), disp.Status().Queue.Dropped.Shutdown)
}

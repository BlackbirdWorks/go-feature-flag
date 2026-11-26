package events_test

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/events"
	"github.com/blackbirdworks/semaphore/exporter"
)

func TestDispatcher_ReloadKeepsUnchangedExporter(t *testing.T) {
	t.Parallel()

	fac := newFactory()
	disp := startDispatcher(t, fac, &events.DispatcherConfig{})

	_, err := disp.Apply(context.Background(), defs("a", "b"))
	require.NoError(t, err)

	for i := range 3 {
		disp.Collect(ev(i))
	}

	fac.of("a", 0).waitFor(t, 3)

	// same names and identities: the definitions are new objects, the exporters are the same
	set, err := disp.Apply(context.Background(), defs("a", "b"))
	require.NoError(t, err)
	assert.Equal(t, events.ReloadSet{Kept: []string{"a", "b"}}, set)

	disp.Collect(ev(3))
	disp.Collect(ev(4))
	fac.of("a", 0).waitFor(t, 2)

	assert.Equal(t, 2, fac.count(), "no manager was built by the reload")
	assert.Equal(t, []string{"flag-0", "flag-1", "flag-2", "flag-3", "flag-4"}, keys(fac.of("a", 0).got()))
	assert.Zero(t, fac.of("a", 0).stopCount(), "the exporter was not stopped")
}

// TestDispatcher_ReloadKeepsQueuedEventsOfRealManager checks with the engine manager that a reload that keeps an
// exporter neither flushes nor loses what the exporter holds: everything is exported once, by the one flush.
func TestDispatcher_ReloadKeepsQueuedEventsOfRealManager(t *testing.T) {
	t.Parallel()

	sink := &nopExporter{}
	cfg := exporter.Config{FlushInterval: time.Hour, MaxEventInMemory: 1000}
	build := func() []events.ExporterDef {
		return []events.ExporterDef{events.NewExporterDef("a", "file", "v1", sink, cfg)}
	}

	disp := events.NewDispatcher(&events.DispatcherConfig{})
	require.NoError(t, disp.Start(context.Background()))

	_, err := disp.Apply(context.Background(), build())
	require.NoError(t, err)

	for i := range 5 {
		require.True(t, disp.Collect(ev(i)))
	}

	set, err := disp.Apply(context.Background(), build())
	require.NoError(t, err)
	assert.Equal(t, []string{"a"}, set.Kept)

	_, err = disp.Shutdown(bounded(t))
	require.NoError(t, err)

	require.Len(t, sink.events, 1, "one flush, the one of the shutdown")
	assert.Len(t, sink.events[0], 5)
	assert.Equal(t, uint64(5), disp.Status().Exporters[0].Exported)
}

func TestDispatcher_ReloadReplacesChangedExporter(t *testing.T) {
	t.Parallel()

	fac := newFactory()
	disp := startDispatcher(t, fac, &events.DispatcherConfig{})

	_, err := disp.Apply(context.Background(), []events.ExporterDef{def("a", "v1")})
	require.NoError(t, err)

	disp.Collect(ev(1))
	disp.Collect(ev(2))
	fac.of("a", 0).waitFor(t, 2)

	set, err := disp.Apply(context.Background(), []events.ExporterDef{def("a", "v2")})
	require.NoError(t, err)
	assert.Equal(t, events.ReloadSet{Replaced: []string{"a"}}, set)

	disp.Collect(ev(3))
	fac.of("a", 1).waitFor(t, 1)

	assert.Equal(t, 1, fac.of("a", 0).stopCount(), "the old exporter was flushed and closed once")
	assert.Zero(t, fac.of("a", 1).stopCount())
	assert.Equal(
		t,
		[]string{"flag-1", "flag-2"},
		keys(fac.of("a", 0).got()),
		"events before the reload went to the old one",
	)
	assert.Equal(t, []string{"flag-3"}, keys(fac.of("a", 1).got()), "events after the reload go to the new one")

	status := disp.Status()
	require.Len(t, status.Exporters, 2)
	assert.Equal(t, events.StateRunning, status.Exporters[0].State)
	assert.Equal(t, events.StateStopped, status.Exporters[1].State)
}

func TestDispatcher_ReloadSets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		first     []events.ExporterDef
		second    []events.ExporterDef
		want      events.ReloadSet
		wantNames []string
	}{
		{
			name:      "add",
			first:     defs("a"),
			second:    defs("a", "b"),
			want:      events.ReloadSet{Kept: []string{"a"}, Added: []string{"b"}},
			wantNames: []string{"a", "b"},
		},
		{
			name:      "remove",
			first:     defs("a", "b"),
			second:    defs("a"),
			want:      events.ReloadSet{Kept: []string{"a"}, Removed: []string{"b"}},
			wantNames: []string{"a", "b"},
		},
		{
			name:      "remove all",
			first:     defs("a", "b"),
			second:    nil,
			want:      events.ReloadSet{Removed: []string{"a", "b"}},
			wantNames: []string{"a", "b"},
		},
		{
			name:   "mixed",
			first:  []events.ExporterDef{def("a", "v1"), def("b", "v1"), def("c", "v1")},
			second: []events.ExporterDef{def("a", "v1"), def("b", "v2"), def("d", "v1")},
			want: events.ReloadSet{
				Kept:     []string{"a"},
				Replaced: []string{"b"},
				Added:    []string{"d"},
				Removed:  []string{"c"},
			},
			wantNames: []string{"a", "b", "d", "b", "c"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fac := newFactory()
			disp := startDispatcher(t, fac, &events.DispatcherConfig{})

			_, err := disp.Apply(context.Background(), tt.first)
			require.NoError(t, err)

			set, err := disp.Apply(context.Background(), tt.second)
			require.NoError(t, err)
			assert.Equal(t, tt.want, set)

			got := make([]string, 0, len(tt.wantNames))
			for _, exp := range disp.Status().Exporters {
				got = append(got, exp.Name)
			}

			assert.ElementsMatch(t, tt.wantNames, got, "running and retired exporters")

			for _, name := range slices.Concat(set.Removed, set.Replaced) {
				assert.Equal(t, 1, fac.of(name, 0).stopCount(), "%s was drained once", name)
			}
		})
	}
}

func TestDispatcher_DrainPastDeadlineIsCutAndCounted(t *testing.T) {
	t.Parallel()

	fac := newFactory()
	fac.stopGate = make(chan struct{})

	disp := startDispatcher(t, fac, &events.DispatcherConfig{})

	_, err := disp.Apply(context.Background(), defs("a"))
	require.NoError(t, err)

	for i := range 4 {
		disp.Collect(ev(i))
	}

	fac.of("a", 0).waitFor(t, 4)

	// the context is done: the drain, which waits for a Stop that is blocked, is cut at once
	set, err := disp.Apply(canceled(), nil)
	require.NoError(t, err)
	assert.Equal(t, events.ReloadSet{Removed: []string{"a"}, Cut: []string{"a"}}, set)

	status := disp.Status()
	require.Len(t, status.Exporters, 1)
	assert.Equal(t, events.StateCut, status.Exporters[0].State)
	assert.Equal(t, uint64(4), status.Exporters[0].Dropped.Drain, "undelivered events are counted")
	assert.Zero(t, status.Exporters[0].Queued)

	close(fac.stopGate) // lets the blocked Stop end
}

func TestDispatcher_ReloadBuildErrorChangesNothing(t *testing.T) {
	t.Parallel()

	fac := newFactory()
	disp := events.NewDispatcher(&events.DispatcherConfig{NewManager: fac.build})
	require.NoError(t, disp.Start(context.Background()))

	t.Cleanup(func() { _, _ = disp.Shutdown(canceled()) })

	_, err := disp.Apply(context.Background(), defs("a"))
	require.NoError(t, err)

	_, err = disp.Reload(context.Background(), events.Specs{Exporters: unknownKindExporter()})
	require.ErrorIs(t, err, events.ErrUnknownKind)

	status := disp.Status()
	require.Len(t, status.Exporters, 1)
	assert.Equal(t, "a", status.Exporters[0].Name)
	assert.Equal(t, events.StateRunning, status.Exporters[0].State)
	assert.Equal(t, 1, fac.count())
}

// TestDispatcher_ConcurrentCollectDuringReload collects from many goroutines while the exporter "a" is replaced
// again and again: each accepted event reaches "b", which stays, once, and "a", whichever generation it is, once.
func TestDispatcher_ConcurrentCollectDuringReload(t *testing.T) {
	t.Parallel()

	const (
		collectors = 8
		perCollect = 400
		reloads    = 25
	)

	fac := newFactory()
	disp := startDispatcher(
		t,
		fac,
		&events.DispatcherConfig{Workers: 4, BatchSize: 8, QueueSize: collectors * perCollect},
	)

	_, err := disp.Apply(context.Background(), []events.ExporterDef{def("a", "g0"), def("b", "v1")})
	require.NoError(t, err)

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		accepted []*exporter.FeatureEvent
	)

	for c := range collectors {
		wg.Go(func() {
			for i := range perCollect {
				event := ev(c*perCollect + i)
				if disp.Collect(event) {
					mu.Lock()

					accepted = append(accepted, event)

					mu.Unlock()
				}
			}
		})
	}

	for i := 1; i <= reloads; i++ {
		_, err = disp.Apply(bounded(t), []events.ExporterDef{def("a", "g"+string(rune('a'+i))), def("b", "v1")})
		require.NoError(t, err)
	}

	wg.Wait()

	_, err = disp.Shutdown(bounded(t))
	require.NoError(t, err)

	gotA := make([]*exporter.FeatureEvent, 0, len(accepted))
	for gen := range fac.generations("a") {
		gotA = append(gotA, fac.of("a", gen).got()...)
	}

	assert.Equal(t, reloads+1, fac.generations("a"))
	assert.Equal(t, keys(accepted), keys(gotA), "each accepted event reached exporter a once, in one generation")
	assert.Equal(t, keys(accepted), keys(fac.of("b", 0).got()), "and exporter b, which stayed, once")
}

func TestDispatcher_ShutdownDrainsAndIsIdempotent(t *testing.T) {
	t.Parallel()

	fac := newFactory()

	var disp *events.Dispatcher

	label := labeledRun(t, func(ctx context.Context) {
		disp = events.NewDispatcher(&events.DispatcherConfig{NewManager: fac.build, Workers: 3})
		require.NoError(t, disp.Start(ctx))
	})

	_, err := disp.Apply(context.Background(), defs("a", "b"))
	require.NoError(t, err)
	assert.Equal(t, 3, labelled(label))

	for i := range 50 {
		require.True(t, disp.Collect(ev(i)))
	}

	report, err := disp.Shutdown(bounded(t))
	require.NoError(t, err)

	for _, name := range []string{"a", "b"} {
		assert.Len(t, fac.of(name, 0).got(), 50, "everything queued was delivered")
		assert.Equal(t, 1, fac.of(name, 0).stopCount(), "%s was flushed once", name)
	}

	assert.Len(t, report.Drained, 2)
	assert.Zero(t, report.Undelivered)
	assert.Equal(t, events.Drops{}, report.Dropped)
	assert.Zero(t, labelled(label), "no goroutine is left")

	// late events are dropped and counted, never queued, and nothing panics
	assert.False(t, disp.Collect(ev(99)))
	assert.False(t, disp.Collect(ev(100)))
	assert.Equal(t, uint64(2), disp.Status().Queue.Dropped.Shutdown)

	again, err := disp.Shutdown(bounded(t))
	require.NoError(t, err)
	assert.Equal(t, report, again, "the second call reports the first")
	assert.Equal(t, 1, fac.of("a", 0).stopCount(), "and flushes nothing again")
	assert.Zero(t, labelled(label))
}

func TestDispatcher_ShutdownWithoutStartDropsTheQueue(t *testing.T) {
	t.Parallel()

	fac := newFactory()
	disp := events.NewDispatcher(&events.DispatcherConfig{NewManager: fac.build})

	_, err := disp.Apply(context.Background(), defs("a"))
	require.NoError(t, err)

	for i := range 3 {
		require.True(t, disp.Collect(ev(i)))
	}

	report, err := disp.Shutdown(bounded(t))
	require.NoError(t, err)
	assert.Equal(t, uint64(3), report.Dropped.Shutdown)
	assert.Empty(t, fac.of("a", 0).got())
	assert.Equal(t, 1, fac.of("a", 0).stopCount())
}

func TestDispatcher_ShutdownPastDeadlineCountsWhatIsLost(t *testing.T) {
	t.Parallel()

	fac := newFactory()
	fac.gate = make(chan struct{})
	fac.stopGate = make(chan struct{})
	fac.entered = make(chan struct{}, 1)

	aborted := make(chan struct{})
	rec, _ := newLogRecorder()
	rec.onLine = func(line string) {
		if strings.Contains(line, "shutdown deadline passed") {
			close(aborted)
		}
	}

	disp := startDispatcher(t, fac, &events.DispatcherConfig{
		Workers: 1, BatchSize: 1, QueueSize: 10, Logger: slog.New(rec),
	})

	_, err := disp.Apply(context.Background(), defs("a"))
	require.NoError(t, err)

	const sent = 25

	accepted := 0

	require.True(t, disp.Collect(ev(-1)))
	await(t, fac.entered, "the worker to block in the manager")

	accepted++

	for i := range sent {
		if disp.Collect(ev(i)) {
			accepted++
		}
	}

	done := make(chan events.ShutdownReport, 1)

	go func() {
		report, _ := disp.Shutdown(canceled())
		done <- report
	}()

	// the shutdown waits for the worker, which is blocked in the manager: let it go once the shutdown is aborted
	await(t, aborted, "the shutdown to pass its deadline")
	close(fac.gate)

	report := await(t, done, "the shutdown")

	close(fac.stopGate)

	delivered := uint64(len(fac.of("a", 0).got()))

	assert.Equal(t, uint64(1), delivered, "only the batch the worker held was delivered")
	assert.Equal(t, uint64(accepted)-1, report.Dropped.Drain, "the queued events are counted as dropped")
	assert.Equal(t, uint64(sent+1-accepted), report.Dropped.QueueFull)
	require.Len(t, report.Drained, 1)
	assert.True(t, report.Drained[0].Cut, "the flush was cut at the deadline")
}

// TestDispatcher_CollectDuringShutdown checks the accounting when collectors race with the shutdown: every event
// is accepted and delivered, or dropped and counted, and nothing panics.
func TestDispatcher_CollectDuringShutdown(t *testing.T) {
	t.Parallel()

	const (
		collectors = 8
		perCollect = 2000
	)

	fac := newFactory()
	disp := startDispatcher(t, fac, &events.DispatcherConfig{Workers: 2, QueueSize: 64})

	_, err := disp.Apply(context.Background(), defs("a"))
	require.NoError(t, err)

	var (
		wg       sync.WaitGroup
		accepted sync.Map
	)

	started := make(chan struct{})

	for c := range collectors {
		wg.Go(func() {
			<-started

			for i := range perCollect {
				event := ev(c*perCollect + i)
				if disp.Collect(event) {
					accepted.Store(event.Key, struct{}{})
				}
			}
		})
	}

	close(started)

	report, err := disp.Shutdown(bounded(t))
	require.NoError(t, err)

	wg.Wait()

	count := 0

	accepted.Range(func(_, _ any) bool {
		count++

		return true
	})

	status := disp.Status()
	total := uint64(collectors * perCollect)

	assert.Zero(t, report.Dropped.Drain)
	assert.Equal(
		t,
		total,
		uint64(count)+status.Queue.Dropped.QueueFull+status.Queue.Dropped.Shutdown,
		"every event is accepted or counted",
	)
	assert.Len(t, fac.of("a", 0).got(), count, "every accepted event was delivered")
	assert.Zero(t, status.Queue.Length, "nothing is left in the channel")
}

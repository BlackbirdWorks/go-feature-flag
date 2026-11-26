package events_test

import (
	"context"
	"slices"
	"strconv"
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
	assert.Equal(t, []string{"flag-0", eventOneKey, eventTwoKey, eventThreeKey, "flag-4"}, keys(fac.of("a", 0).got()))
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

	disp := newDispatcher(t, &events.DispatcherConfig{})
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

	flushed := make([]string, 0, len(sink.events[0]))
	for _, event := range sink.events[0] {
		flushed = append(flushed, event.GetKey())
	}

	slices.Sort(flushed)
	assert.Equal(t, eventKeys(5), flushed)
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
		[]string{eventOneKey, eventTwoKey},
		keys(fac.of("a", 0).got()),
		"events before the reload went to the old one",
	)
	assert.Equal(t, []string{eventThreeKey}, keys(fac.of("a", 1).got()), "events after the reload go to the new one")

	require.NoError(t, disp.WaitDrains(bounded(t)))

	status := disp.Status()
	require.Len(t, status.Exporters, 1, "one entry per exporter name")
	assert.Equal(t, events.StateRunning, status.Exporters[0].State, "the state of the current instance")
	assert.Equal(t, uint64(3), status.Exporters[0].Handed, "the counters of both instances")
	assert.Equal(t, uint64(2), status.Exporters[0].Exported, "the old instance was flushed")
	assertExporterAccounting(t, &status, "after the replacement")
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
			wantNames: []string{"a", "b", "c", "d"},
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

// TestDispatcher_CutDrainReconcilesWhenTheExporterFinishesLate cuts the drain of an exporter whose Stop is
// blocked, then lets it finish: the events are never exported and counted as dropped at the same time, at any
// point: they are pending while the exporter is cut, and exported or dropped (once) when it finishes.
func TestDispatcher_CutDrainReconcilesWhenTheExporterFinishesLate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		flushErr       error
		name           string
		wantLastErrors events.ErrorClass
		wantExported   uint64
		wantDrain      uint64
	}{
		{name: "the late flush succeeds", wantExported: 4, wantDrain: 0, wantLastErrors: events.ErrorNone},
		{
			name: "the late flush fails", flushErr: assert.AnError, wantExported: 0, wantDrain: 4,
			wantLastErrors: events.ErrorOther,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fac := newFactory()
			fac.stopGate = make(chan struct{})

			disp := newDispatcher(t, &events.DispatcherConfig{NewManager: fac.build, DrainTimeout: time.Nanosecond})
			require.NoError(t, disp.Start(context.Background()))

			sink := &nopExporter{err: tt.flushErr}
			old := events.NewExporterDef("a", "file", "v1", sink, exporter.Config{})

			_, err := disp.Apply(context.Background(), []events.ExporterDef{old})
			require.NoError(t, err)

			for i := range 4 {
				require.True(t, disp.Collect(ev(i)))
			}

			fac.of("a", 0).waitFor(t, 4)

			// the drain deadline is 1 ns and Stop is blocked: the drain is cut
			set, err := disp.Apply(context.Background(), nil)
			require.NoError(t, err)
			assert.Equal(t, events.ReloadSet{Removed: []string{"a"}, Cut: []string{"a"}}, set)

			cut := disp.Status()
			require.Len(t, cut.Exporters, 1)
			assert.Equal(t, events.StateCut, cut.Exporters[0].State)
			assert.Equal(
				t,
				int64(4),
				cut.Exporters[0].Queued,
				"the events are pending: neither exported nor dropped yet",
			)
			assert.Zero(t, cut.Exporters[0].Dropped.Drain)
			assert.Zero(t, cut.Exporters[0].Exported)
			assertExporterAccounting(t, &cut, "while cut")

			close(fac.stopGate)
			require.NoError(t, disp.WaitDrains(bounded(t)))

			late := disp.Status()
			require.Len(t, late.Exporters, 1)
			assert.Equal(t, events.StateStopped, late.Exporters[0].State)
			assert.Equal(t, tt.wantExported, late.Exporters[0].Exported)
			assert.Equal(t, tt.wantDrain, late.Exporters[0].Dropped.Drain)
			assert.Zero(t, late.Exporters[0].Queued)
			assert.Equal(t, tt.wantLastErrors, late.Exporters[0].LastError)
			assertExporterAccounting(t, &late, "after the late completion")

			// a second look after the shutdown changes nothing
			_, err = disp.Shutdown(bounded(t))
			require.NoError(t, err)

			final := disp.Status()
			assert.Equal(t, late.Exporters, final.Exporters)
			assertExporterAccounting(t, &final, "after the shutdown")
		})
	}
}

// TestDispatcher_CutDrainWithTheEngineManager is the same with the real engine manager: its Stop blocks in the
// flush of an exporter that waits for a gate.
func TestDispatcher_CutDrainWithTheEngineManager(t *testing.T) {
	t.Parallel()

	gate := make(chan struct{})
	slow := &gatedExporter{gate: gate}

	newManager, tap := tapped()
	disp := newDispatcher(t, &events.DispatcherConfig{NewManager: newManager, DrainTimeout: time.Nanosecond})
	require.NoError(t, disp.Start(context.Background()))

	_, err := disp.Apply(context.Background(), []events.ExporterDef{
		events.NewExporterDef(
			"a",
			"file",
			"v1",
			slow,
			exporter.Config{FlushInterval: time.Hour, MaxEventInMemory: 1000},
		),
	})
	require.NoError(t, err)

	for i := range 10 {
		require.True(t, disp.Collect(ev(i)))
	}

	for range 10 {
		await(t, tap, "the events to reach the engine manager")
	}

	set, err := disp.Apply(context.Background(), nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"a"}, set.Cut)

	cut := disp.Status()
	assert.Equal(t, events.StateCut, cut.Exporters[0].State)
	assert.Zero(t, cut.Exporters[0].Dropped.Drain)
	assertExporterAccounting(t, &cut, "while cut")

	close(gate)
	require.NoError(t, disp.WaitDrains(bounded(t)))

	late := disp.Status()
	assert.Equal(t, uint64(10), late.Exporters[0].Exported)
	assert.Equal(t, uint64(10), slow.exported.Load())
	assert.Zero(t, late.Exporters[0].Dropped.Drain)
	assertExporterAccounting(t, &late, "after the late completion")
	assertGlobalAccounting(t, &late, late.Exporters[0].Handed, "after the late completion")
}

// TestDispatcher_ReloadToNoExporterCountsTheEvents checks that the events a worker takes while the set is empty are
// counted, so that every accepted event is exported or counted.
func TestDispatcher_ReloadToNoExporterCountsTheEvents(t *testing.T) {
	t.Parallel()

	fac := newFactory()
	disp := newDispatcher(t, &events.DispatcherConfig{NewManager: fac.build})

	_, err := disp.Apply(context.Background(), defs("a"))
	require.NoError(t, err)

	// the workers do not run: the events wait in the channel while the set becomes empty
	for i := range 7 {
		require.True(t, disp.Collect(ev(i)))
	}

	set, err := disp.Apply(context.Background(), nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"a"}, set.Removed)

	require.NoError(t, disp.Start(context.Background()))

	report, err := disp.Shutdown(bounded(t))
	require.NoError(t, err)

	assert.Equal(t, uint64(7), report.Dropped.NoExporter, "the batch taken with an empty set is counted")
	assert.Empty(t, fac.of("a", 0).got())

	status := disp.Status()
	assert.Equal(t, uint64(7), status.Queue.Accepted)
	assertGlobalAccounting(t, &status, 0, "after the shutdown")
}

// TestDispatcher_CancelledReloadDoesNotCutTheDrains checks that the drain deadline does not come from the
// context of the reload: a request that was cancelled does not cut a drain that would end in time.
func TestDispatcher_CancelledReloadDoesNotCutTheDrains(t *testing.T) {
	t.Parallel()

	fac := newFactory()
	fac.stopGate = make(chan struct{})
	fac.stopEntered = make(chan struct{}, 1)

	disp := startDispatcher(t, fac, &events.DispatcherConfig{DrainTimeout: time.Hour})

	_, err := disp.Apply(context.Background(), defs("a"))
	require.NoError(t, err)

	done := make(chan events.ReloadSet, 1)

	go func() {
		set, _ := disp.Apply(canceled(), nil)
		done <- set
	}()

	// the drain is under way, Stop is blocked, and the context of the reload is done
	await(t, fac.stopEntered, "the drain to call Stop")
	close(fac.stopGate)

	assert.Equal(t, events.ReloadSet{Removed: []string{"a"}}, await(t, done, "the reload"), "no drain was cut")
}

// TestDispatcher_ShutdownDoesNotWaitForAReload checks that a shutdown starts at once and honours its own deadline
// while a reload waits for an exporter that hangs.
func TestDispatcher_ShutdownDoesNotWaitForAReload(t *testing.T) {
	t.Parallel()

	fac := newFactory()
	fac.stopGate = make(chan struct{})
	fac.stopEntered = make(chan struct{}, 1)

	disp := newDispatcher(t, &events.DispatcherConfig{NewManager: fac.build, DrainTimeout: time.Hour})
	require.NoError(t, disp.Start(context.Background()))

	_, err := disp.Apply(context.Background(), defs("a", "b"))
	require.NoError(t, err)

	reloaded := make(chan events.ReloadSet, 1)

	go func() {
		// "a" leaves and its Stop hangs: the reload waits for it (for an hour)
		set, _ := disp.Apply(context.Background(), defs("b"))
		reloaded <- set
	}()

	await(t, fac.stopEntered, "the drain of the reload to call Stop")

	finished := make(chan events.ShutdownReport, 1)

	go func() {
		report, _ := disp.Shutdown(canceled())
		finished <- report
	}()

	report := await(t, finished, "the shutdown, which must not wait for the reload")

	assert.Len(t, report.Drained, 2, "the exporter that was leaving and the one that stayed")

	for _, res := range report.Drained {
		assert.True(t, res.Cut, "%s: the deadline of the shutdown had passed", res.Name)
	}

	close(fac.stopGate)
	assert.Equal(t, []string{"a"}, await(t, reloaded, "the reload").Removed)
	require.NoError(t, disp.WaitDrains(bounded(t)))
}

// TestDispatcher_CountersSurviveReloads replaces the same exporter several times: its totals only grow, and
// they reconcile with the accepted events.
func TestDispatcher_CountersSurviveReloads(t *testing.T) {
	t.Parallel()

	const (
		generations = 4
		perGen      = 25
	)

	fac := newFactory()
	disp := startDispatcher(t, fac, &events.DispatcherConfig{})

	var previous events.ExporterStatus

	for gen := range generations {
		_, err := disp.Apply(context.Background(), []events.ExporterDef{def("a", "gen-"+strconv.Itoa(gen))})
		require.NoError(t, err)

		for i := range perGen {
			require.True(t, disp.Collect(ev(gen*perGen+i)))
		}

		fac.of("a", gen).waitFor(t, perGen)
		require.NoError(t, disp.WaitDrains(bounded(t)))

		status := disp.Status()
		current := statusOf(t, &status, "a")

		assert.Equal(t, uint64((gen+1)*perGen), current.Handed, "generation %d: handed is cumulative", gen)
		assert.GreaterOrEqual(t, current.Handed, previous.Handed)
		assert.GreaterOrEqual(t, current.Exported, previous.Exported, "exported never goes down")
		assert.GreaterOrEqual(t, current.Flushes, previous.Flushes)
		assertExporterAccounting(t, &status, "after generation "+strconv.Itoa(gen))
		assertGlobalAccounting(t, &status, current.Handed, "after generation "+strconv.Itoa(gen))

		previous = current
	}

	_, err := disp.Shutdown(bounded(t))
	require.NoError(t, err)

	status := disp.Status()
	final := statusOf(t, &status, "a")
	assert.Equal(t, uint64(generations*perGen), final.Exported, "every generation was flushed")
	assertExporterAccounting(t, &status, "after the shutdown")
}

func TestDispatcher_ReloadBuildErrorChangesNothing(t *testing.T) {
	t.Parallel()

	fac := newFactory()
	disp := newDispatcher(t, &events.DispatcherConfig{NewManager: fac.build})
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

	disp := newDispatcher(t, &events.DispatcherConfig{NewManager: fac.build, Workers: 3})

	label := labeledRun(t, func(ctx context.Context) {
		require.NoError(t, disp.Start(ctx))

		_, err := disp.Apply(ctx, defs("a", "b"))
		require.NoError(t, err)
	})

	assert.Equal(t, 3+2, labelled(label), "the workers and one goroutine per exporter")

	for i := range 50 {
		require.True(t, disp.Collect(ev(i)))
	}

	report, err := disp.Shutdown(bounded(t))
	require.NoError(t, err)

	for _, name := range []string{"a", "b"} {
		assert.Equal(t, eventKeys(50), keys(fac.of(name, 0).got()), "everything queued was delivered")
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
	disp := newDispatcher(t, &events.DispatcherConfig{NewManager: fac.build})

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

func TestDispatcher_ShutdownPastDeadlineCutsTheFlushAndReconcilesLater(t *testing.T) {
	t.Parallel()

	fac := newFactory()
	fac.stopGate = make(chan struct{})

	disp := newDispatcher(t, &events.DispatcherConfig{NewManager: fac.build})
	require.NoError(t, disp.Start(context.Background()))

	_, err := disp.Apply(context.Background(), defs("a"))
	require.NoError(t, err)

	for i := range 6 {
		require.True(t, disp.Collect(ev(i)))
	}

	fac.of("a", 0).waitFor(t, 6)

	report, err := disp.Shutdown(canceled())
	require.NoError(t, err)
	require.Len(t, report.Drained, 1)
	assert.True(t, report.Drained[0].Cut, "the flush was cut at the deadline")
	assert.Equal(t, int64(6), report.Drained[0].Undelivered, "the events are pending")
	assert.Equal(t, int64(6), report.Undelivered)

	cut := disp.Status()
	assert.Equal(t, events.StateCut, cut.Exporters[0].State)
	assertExporterAccounting(t, &cut, "after the cut shutdown")

	close(fac.stopGate)
	require.NoError(t, disp.WaitDrains(bounded(t)))

	late := disp.Status()
	assert.Equal(t, events.StateStopped, late.Exporters[0].State)
	assert.Equal(t, uint64(6), late.Exporters[0].Exported, "delivered after all, and counted once")
	assert.Zero(t, late.Exporters[0].Dropped.Drain)
	assertExporterAccounting(t, &late, "after the late completion")
	assertGlobalAccounting(t, &late, late.Exporters[0].Handed, "after the late completion")
}

// TestDispatcher_ShutdownWithAnExpiredDeadlineAccountsForEveryEvent shuts down with a deadline that has passed
// while events are still moving: each accepted event was handed to the exporter or counted as dropped (drain).
func TestDispatcher_ShutdownWithAnExpiredDeadlineAccountsForEveryEvent(t *testing.T) {
	t.Parallel()

	const sent = 50_000

	fac := newFactory()
	disp := startDispatcher(t, fac, &events.DispatcherConfig{QueueSize: sent, BatchSize: 8})

	_, err := disp.Apply(context.Background(), defs("a"))
	require.NoError(t, err)

	for i := range sent {
		require.True(t, disp.Collect(ev(i)))
	}

	report, err := disp.Shutdown(canceled())
	require.NoError(t, err)

	status := disp.Status()
	handed := statusOf(t, &status, "a").Handed

	assert.Equal(t, uint64(sent), handed+report.Dropped.Drain, "every accepted event was handed over or counted")
	assertGlobalAccounting(t, &status, handed, "after the shutdown")
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

	acceptedKeys := []string{}

	accepted.Range(func(key, _ any) bool {
		keyString, _ := key.(string)
		acceptedKeys = append(acceptedKeys, keyString)

		return true
	})

	slices.Sort(acceptedKeys)

	count := len(acceptedKeys)

	status := disp.Status()
	total := uint64(collectors * perCollect)

	assert.Zero(t, report.Dropped.Drain)
	assert.Equal(
		t,
		total,
		uint64(count)+status.Queue.Dropped.QueueFull+status.Queue.Dropped.Shutdown,
		"every event is accepted or counted",
	)
	assert.Equal(t, acceptedKeys, keys(fac.of("a", 0).got()), "every accepted event was delivered")
	assert.Zero(t, status.Queue.Length, "nothing is left in the channel")
}

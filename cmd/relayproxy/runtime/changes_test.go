package runtime_test

import (
	goruntime "runtime"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/runtime"
	"github.com/blackbirdworks/semaphore/controlplane"
)

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}

	slices.Sort(out)

	return out
}

// drain reads exactly n events.
func drain(t *testing.T, r *rig, n int) []runtime.Change {
	t.Helper()

	out := make([]runtime.Change, 0, n)
	for range n {
		out = append(out, <-r.eng.Changes())
	}

	return out
}

func changeSetObjs(t *testing.T) objs {
	t.Helper()

	return objs{
		"f1": obj(runtime.KindFlags, join(flagsYAML(1, "x", "y", "w"))),
		"f2": flagsObj(1, "z"),
		setsName: setsObj(t,
			fset("a", src("f1"), keyA),
			fset("b", src("f1", "f2"), keyB),
			fset("c", src("f2"), "key-c"),
		),
	}
}

func TestEngineEmitsOneChangePerRebuiltFlagSet(t *testing.T) {
	t.Parallel()

	r := newRig(t, runtime.Hooks{})
	r.start()

	base := changeSetObjs(t)
	r.feed(content(1, base))

	first := drain(t, r, 3)

	assert.Equal(t, []string{"a", "b", "c"}, []string{first[0].FlagSet, first[1].FlagSet, first[2].FlagSet})
	assert.Equal(t, []string{"w", "x", "y"}, sortedKeys(first[0].Diff.Added), "a new flag set adds all its flags")
	assert.Equal(t, []string{"w", "x", "y", "z"}, sortedKeys(first[1].Diff.Added))
	assert.Equal(t, uint64(1), first[0].Generation)
	assert.Equal(t, uint64(1), first[0].Snapshot)

	r.feed(content(2, base.with(objs{
		"f1": obj(runtime.KindFlags, join(flagsYAML(2, "x"), flagsYAML(1, "w"), flagsYAML(1, "n"))),
	})))

	second := drain(t, r, 2)

	for _, change := range second {
		assert.Contains(t, []string{"a", "b"}, change.FlagSet)
		assert.False(t, change.Removed)
		assert.Equal(t, []string{"n"}, sortedKeys(change.Diff.Added), "added in %s", change.FlagSet)
		assert.Equal(t, []string{"y"}, sortedKeys(change.Diff.Deleted), "deleted in %s", change.FlagSet)
		assert.Equal(
			t,
			[]string{"x"},
			sortedKeys(change.Diff.Updated),
			"updated in %s: the unchanged flag w is not listed",
			change.FlagSet,
		)
		assert.Equal(t, uint64(2), change.Generation)
		assert.Equal(t, uint64(2), change.Snapshot)
	}

	assert.Equal(t, []string{"a", "b"}, []string{second[0].FlagSet, second[1].FlagSet})
	assert.Empty(t, r.eng.Changes(), "the flag set c was not rebuilt: no event")

	before := second[0].Diff.Updated["x"]

	assert.EqualValues(t, 1, before.Before.GetVariationValue("v"))
	assert.EqualValues(t, 2, before.After.GetVariationValue("v"))
}

func TestEngineChangeForAWithdrawnFlagSet(t *testing.T) {
	t.Parallel()

	r := newRig(t, runtime.Hooks{})
	r.start()

	base := changeSetObjs(t)
	r.feed(content(1, base))
	drain(t, r, 3)

	r.feed(content(2, base.with(objs{setsName: setsObj(t,
		fset("b", src("f1", "f2"), keyB), fset("c", src("f2"), "key-c"))})))

	got := drain(t, r, 1)[0]

	assert.Equal(t, "a", got.FlagSet)
	assert.True(t, got.Removed)
	assert.Equal(t, []string{"w", "x", "y"}, sortedKeys(got.Diff.Deleted))
	assert.Empty(t, got.Diff.Added)
}

func TestEngineStateOnlyChangesProduceNoEvent(t *testing.T) {
	t.Parallel()

	r := newRig(t, runtime.Hooks{})
	r.start()

	base := changeSetObjs(t)
	r.feed(content(1, base))
	drain(t, r, 3)

	// f1 goes stale: flag sets a and b are rebuilt (they are degraded now) but no flag changes
	res := r.feed(content(2, base.with(objs{"f1": {Kind: runtime.KindFlags, State: runtime.ObjectStale}})))

	assert.Equal(t, runtime.OutcomeApplied, res.Outcome)
	assert.Equal(t, runtime.StateDegraded, r.holder.Load().FlagSets["a"].State)

	r.feed(content(3, base.with(objs{
		"f1": {Kind: runtime.KindFlags, State: runtime.ObjectStale}, "f2": flagsObj(5, "z"),
	})))

	got := drain(t, r, 2)

	assert.Equal(
		t,
		[]string{"b", "c"},
		[]string{got[0].FlagSet, got[1].FlagSet},
		"no event for the state-only rebuild of a",
	)
	assert.Empty(t, r.eng.Changes())
}

func TestEngineSlowConsumerCoalescesPerFlagSet(t *testing.T) {
	t.Parallel()

	const sets = 5

	flagSets := make([]controlplane.FlagSet, 0, sets)
	for _, name := range []string{"s1", "s2", "s3", "s4", "s5"} {
		flagSets = append(flagSets, fset(name, src("f1"), "key-"+name))
	}

	base := objs{"f1": flagsObj(1, "x"), setsName: setsObj(t, flagSets...)}

	r := newRig(t, runtime.Hooks{}, runtime.WithChangeBuffer(1))
	r.start()

	// nobody reads the events: the rounds must still complete
	r.feed(content(1, base))

	// one event sits in the channel, the other four wait in the pending table
	for len(r.eng.Changes()) < 1 {
		goruntime.Gosched()
	}

	r.feed(content(2, base.with(objs{"f1": flagsObj(2, "x")})))
	r.feed(content(3, base.with(objs{"f1": flagsObj(3, "x")})))

	first := r.holder.Load()

	require.Equal(t, uint64(3), first.Generation, "the engine was not blocked by the slow consumer")

	// s1 was already in the channel (generation 1); s2..s5 were pending and are merged to one event each;
	// s1 has a new pending event for generations 2 and 3 merged
	got := drain(t, r, 6)

	counts := map[string]int{}
	for _, ch := range got {
		counts[ch.FlagSet]++
	}

	assert.Equal(
		t,
		map[string]int{"s1": 2, "s2": 1, "s3": 1, "s4": 1, "s5": 1},
		counts,
		"no flag set is lost, none is repeated",
	)
	assert.Equal(t, "s1", got[0].FlagSet)
	assert.Equal(t, uint64(1), got[0].Generation)

	for _, ch := range got[1:] {
		assert.Equal(t, uint64(3), ch.Generation, "the pending events of %s were merged up to generation 3", ch.FlagSet)
	}

	// s2: pending as "added x (value 1)" at generation 1, then updated twice: the merge is an addition of the newest
	byName := map[string]runtime.Change{}
	for _, ch := range got[1:] {
		byName[ch.FlagSet] = ch
	}

	assert.Equal(t, []string{"x"}, sortedKeys(byName["s2"].Diff.Added), "added then updated is still an addition")
	assert.EqualValues(t, 3, byName["s2"].Diff.Added["x"].GetVariationValue("v"), "with the newest definition")
	assert.Empty(t, byName["s2"].Diff.Updated)

	// s1: its first event is in the channel already; the later two generations merge to one update 1 to 3
	var again runtime.Change

	for _, change := range got[1:] {
		if change.FlagSet == "s1" {
			again = change
		}
	}

	update := again.Diff.Updated["x"]

	assert.EqualValues(t, 1, update.Before.GetVariationValue("v"))
	assert.EqualValues(t, 3, update.After.GetVariationValue("v"))
}

func TestEngineChangesCompose(t *testing.T) {
	t.Parallel()

	// the consumer is stalled for two rounds: the pending event goes from the first state to the last one
	tests := []struct {
		name        string
		second      objs
		third       objs
		wantAdded   []string
		wantDeleted []string
		wantUpdated []string
		wantNone    bool
	}{
		{
			name:     "added then deleted is nothing",
			second:   objs{"f1": flagsObj(1, "x", "k", "n")},
			third:    objs{"f1": flagsObj(1, "x", "k")},
			wantNone: true,
		},
		{
			name:        "updated then updated is one update from the first state to the last",
			second:      objs{"f1": flagsObj(2, "x", "k")},
			third:       objs{"f1": obj(runtime.KindFlags, join(flagsYAML(3, "x"), flagsYAML(2, "k")))},
			wantUpdated: []string{"k", "x"},
		},
		{
			name:        "deleted then added back is an update to the new pointer",
			second:      objs{"f1": flagsObj(1, "k")},
			third:       objs{"f1": flagsObj(1, "x", "k")},
			wantUpdated: []string{"x"},
		},
		{
			name:        "deleted stays deleted",
			second:      objs{"f1": flagsObj(1, "k")},
			third:       objs{"f1": flagsObj(1, "k")},
			wantDeleted: []string{"x"},
		},
		{
			name:      "added then updated is an addition",
			second:    objs{"f1": flagsObj(1, "x", "k", "n")},
			third:     objs{"f1": obj(runtime.KindFlags, join(flagsYAML(1, "x", "k"), flagsYAML(2, "n")))},
			wantAdded: []string{"n"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sets := setsObj(t, fset("a", src("f1"), keyA), fset("b", src("f2"), keyB))
			first := objs{"f1": flagsObj(1, "x", "k"), "f2": flagsObj(1, "q"), setsName: sets}

			r := newRig(t, runtime.Hooks{}, runtime.WithChangeBuffer(1))
			r.start()

			// the first generation fills the channel with the event of a; b stays pending
			r.feed(content(1, first))

			for len(r.eng.Changes()) < 1 {
				goruntime.Gosched()
			}

			// b is never touched again; a changes twice while its first event is still in the channel
			r.feed(content(2, first.with(tt.second)))
			r.feed(content(3, first.with(tt.third)))

			// a: the event in the channel, then the merged pending event of generations 2 and 3
			wantEvents := 3
			if tt.wantNone {
				wantEvents = 2
			}

			events := drain(t, r, wantEvents)

			var merged *runtime.Change

			for i := range events {
				if events[i].FlagSet == "a" && events[i].Generation > 1 {
					merged = &events[i]
				}
			}

			if tt.wantNone {
				assert.Nil(t, merged, "a change that is undone has no event")

				return
			}

			require.NotNil(t, merged)
			assert.Equal(t, tt.wantAdded, nilIfEmpty(sortedKeys(merged.Diff.Added)))
			assert.Equal(t, tt.wantDeleted, nilIfEmpty(sortedKeys(merged.Diff.Deleted)))
			assert.Equal(t, tt.wantUpdated, nilIfEmpty(sortedKeys(merged.Diff.Updated)))
		})
	}
}

func nilIfEmpty(in []string) []string {
	if len(in) == 0 {
		return nil
	}

	return in
}

func TestEngineShutdownLeavesNoGoroutine(t *testing.T) {
	t.Parallel()

	r := newRig(t, runtime.Hooks{})
	r.start()

	r.feed(content(1, simpleObjs(t, 1)))

	assert.Equal(t, 1, r.labelledGoroutines(), "the detector sees the running engine")

	r.stop()

	assert.Equal(t, 0, r.labelledGoroutines(), "no goroutine started by the engine is left after Run returned")

	_, open := <-r.eng.Changes()
	for open {
		_, open = <-r.eng.Changes()
	}

	assert.False(t, open, "the change channel is closed")
}

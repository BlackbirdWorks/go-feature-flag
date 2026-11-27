package flagruntime_test

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/ffcontext"
)

// TestScaleOneObjectChange: a change of some flags objects rebuilds exactly the flag sets that list them. Every other
// flag set keeps its pointer, and an object is parsed once per checksum.
func TestScaleOneObjectChange(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		changed     []int
		wantRebuilt []string
		wantParsed  []string
	}{
		{
			name: "one object", changed: []int{3},
			wantRebuilt: scaleSetNames(3), wantParsed: []string{scaleObjectName(3)},
		},
		{
			name: "two objects", changed: []int{0, scaleSets/scaleSetsPerObject - 1},
			wantRebuilt: scaleSetNames(0, scaleSets/scaleSetsPerObject-1),
			wantParsed:  []string{scaleObjectName(0), scaleObjectName(scaleSets/scaleSetsPerObject - 1)},
		},
		{name: "no object", changed: nil, wantRebuilt: []string{}, wantParsed: []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var parsed []string

			builder := flagruntime.NewBuilder(newService(t), flagruntime.BuilderOptions{
				OnParse: func(ev flagruntime.ParseEvent) { parsed = append(parsed, ev.Name) },
			})

			first := buildCommitted(t, builder, scaleVariant(t, scaleSets))
			assert.Len(t, parsed, scaleSets/scaleSetsPerObject+1, "every object once, and the sets document")

			parsed = nil
			second := buildCommitted(t, builder, scaleVariant(t, scaleSets, tt.changed...))

			assert.Equal(t, tt.wantRebuilt, nonNil(sortedStrings(rebuiltNames(second))))
			assert.Equal(t, tt.wantParsed, nonNil(sortedStrings(parsed)))
			assert.Equal(t, len(tt.wantRebuilt), second.Stats.Rebuilt)
			assert.Equal(t, scaleSets-len(tt.wantRebuilt), second.Stats.Reused)
			assert.Equal(t, len(tt.wantParsed), second.Stats.Parsed)

			rebuilt := map[string]bool{}
			for _, name := range tt.wantRebuilt {
				rebuilt[name] = true
			}

			// pointer identity of every flag set: the names that changed pointer are exactly the rebuilt ones
			var moved []string

			for name, old := range first.Runtime.FlagSets {
				if second.Runtime.FlagSets[name] != old {
					moved = append(moved, name)
				}
			}

			assert.Equal(t, tt.wantRebuilt, nonNil(sortedStrings(moved)))
			assert.Len(t, second.Runtime.FlagSets, scaleSets)
		})
	}
}

func nonNil(in []string) []string {
	if in == nil {
		return []string{}
	}

	return in
}

// allocsOfOneObjectChange returns the allocations and bytes of the build of a one-object change over a fleet.
func allocsOfOneObjectChange(tb testing.TB, sets int) (uint64, uint64) {
	tb.Helper()

	base, next := scaleVariant(tb, sets), scaleVariant(tb, sets, 0)
	builder := flagruntime.NewBuilder(newService(tb), flagruntime.BuilderOptions{})
	buildCommitted(tb, builder, base)

	var before, after runtime.MemStats

	runtime.GC()
	runtime.ReadMemStats(&before)

	built, err := builder.Build(tb.Context(), next)

	runtime.ReadMemStats(&after)
	require.NoError(tb, err)
	require.Len(tb, built.Rebuilt, scaleSetsPerObject)

	return after.Mallocs - before.Mallocs, after.TotalAlloc - before.TotalAlloc
}

// TestScaleChangeCostIsProportionalToTheChange compares the same one-object change (ten flag sets of 100 flags) in a
// small fleet and in a large one. The work on the changed sets is identical, so the allocation count must stay
// flat (the map copies of the name tables are a few allocations, not one per flag set): at most 1.25x for a fleet
// 10x larger. What grows with the fleet is the bytes of the copied name tables, a few words per unchanged flag
// set: each extra unchanged flag set may cost at most 1/50 of the bytes of rebuilding one changed flag set, where
// a change that touched the whole fleet would cost 50 times that. The test reads the allocation counters of the
// whole process, so it runs alone: a parallel test would add its own allocations to the counters.
//
//nolint:paralleltest // reads process wide allocation counters, which a parallel test would pollute
func TestScaleChangeCostIsProportionalToTheChange(t *testing.T) {
	const fleetFactor = 10

	small := scaleSets / fleetFactor

	smallAllocs, smallBytes := allocsOfOneObjectChange(t, small)
	largeAllocs, largeBytes := allocsOfOneObjectChange(t, scaleSets)

	t.Logf("one object change: %d sets %d allocs %d bytes; %d sets %d allocs %d bytes",
		small, smallAllocs, smallBytes, scaleSets, largeAllocs, largeBytes)

	assert.LessOrEqual(t, largeAllocs, smallAllocs+smallAllocs/4, "allocations follow the change, not the fleet")

	rebuildBytes := smallBytes / scaleSetsPerObject
	extraBytes := (largeBytes - smallBytes) / uint64(scaleSets-small)

	assert.LessOrEqual(t, extraBytes*50, rebuildBytes, "an unchanged flag set costs a fraction of a rebuilt one")
}

// TestScaleFullGenerationSwap: a generation that changes every object replaces every flag set in one swap, and a
// reader that loads the runtime sees one generation in every flag set.
func TestScaleFullGenerationSwap(t *testing.T) {
	t.Parallel()

	harness := newRig(t, flagruntime.Hooks{})
	harness.start()

	first := scaleGeneration(t, 1, scaleSets)
	harness.feed(first)

	before := harness.holder.Load()
	assert.Empty(t, mixedSets(before, 1, scaleSets))

	res := harness.feed(scaleGeneration(t, 2, scaleSets))
	after := harness.holder.Load()

	assert.Equal(t, flagruntime.OutcomeApplied, res.Outcome)
	assert.Equal(t, before.Snapshot+1, after.Snapshot, "one swap")
	assert.Equal(t, uint64(2), after.Generation)
	assert.Len(t, after.FlagSets, scaleSets)
	assert.Empty(t, mixedSets(after, 2, scaleSets), "every flag set carries the values of generation 2")

	var kept []string

	for name, old := range before.FlagSets {
		if after.FlagSets[name] == old {
			kept = append(kept, name)
		}
	}

	assert.Empty(t, kept, "every flag set was replaced")
	assert.Empty(t, mixedSets(before, 1, scaleSets), "the runtime a reader holds is not changed by the swap")
}

// mixedSets lists the flag sets, out of the sets the test generated, that are missing from the runtime or whose
// flags are not exactly the flags of generation gen (every flag, none more): the consistency fingerprint of a runtime.
func mixedSets(run *flagruntime.Runtime, gen uint64, sets int) []string {
	var bad []string

	for idx := range sets / scaleSetsPerObject {
		for slot := range scaleSetsPerObject {
			name := scaleSetName(idx, slot)

			set, ok := run.FlagSets[name]
			if !ok || len(set.Flags) != scaleFlagsPerSet || !carries(set, scaleValue(gen, idx)) {
				bad = append(bad, name)
			}
		}
	}

	return bad
}

// carries reports whether every flag of the set has the value want.
func carries(set *flagruntime.FlagSet, want int) bool {
	for i := range scaleFlagsPerSet {
		if value(set, fmt.Sprintf("flag-%03d", i)) != want {
			return false
		}
	}

	return true
}

// TestScaleGoroutinesDoNotGrowWithFlagSets: the engine is one goroutine whatever the number of flag sets. The rig
// of every row carries its own pprof label, so only the goroutines of that engine are counted.
func TestScaleGoroutinesDoNotGrowWithFlagSets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		sets           int
		wantGoroutines int
	}{
		{name: "one object", sets: scaleSetsPerObject, wantGoroutines: 1},
		{name: "a tenth of the fleet", sets: scaleSets / 10, wantGoroutines: 1},
		{name: "the fleet", sets: scaleSets, wantGoroutines: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			harness := newRig(t, flagruntime.Hooks{})
			harness.start()

			for gen := uint64(1); gen <= 2; gen++ {
				require.Equal(t, flagruntime.OutcomeApplied, harness.feed(scaleGeneration(t, gen, tt.sets)).Outcome)
			}

			require.Len(t, harness.holder.Load().FlagSets, tt.sets, "the fleet of the row is loaded")
			assert.Empty(t, mixedSets(harness.holder.Load(), 2, tt.sets))
			assert.Equal(t, tt.wantGoroutines, harness.labelledGoroutines())
		})
	}
}

// scalePositions lists the positions a reader that starts at start visits.
func scalePositions(sets, stride, start int) []int {
	out := make([]int, 0, (sets-start+stride-1)/stride)

	for pos := start; pos < sets; pos += stride {
		out = append(out, pos)
	}

	return out
}

// TestScaleReadersDuringContinuousReloads: readers evaluate through the evaluator while the engine swaps whole
// generations. No evaluation fails, and the value of every result is the one of the generation of the runtime the
// reader loaded (the generation is in the value, see scaleValue), whatever swaps happen during the pass.
func TestScaleReadersDuringContinuousReloads(t *testing.T) {
	t.Parallel()

	const (
		readers     = 4
		generations = 8
		stride      = 37
	)

	// the swaps are whole generations: a smaller fleet than the full one keeps the number of swaps affordable
	sets := min(scaleSets, 500)

	harness := newRig(t, flagruntime.Hooks{})
	harness.start()
	harness.feed(scaleGeneration(t, 1, sets))

	eval := flagruntime.NewEvaluator(newService(t), nil)

	var (
		stop      atomic.Bool
		wg        sync.WaitGroup
		evaluated atomic.Int64
		pinned    atomic.Int64
	)

	// pass evaluates every stride-th flag set of a runtime, from position reader; the values must be the ones of the
	// generation of that runtime. It returns the number of results it checked.
	pass := func(run *flagruntime.Runtime, reader int) int64 {
		ctx := ffcontext.NewEvaluationContext("user")

		var checked int64

		for pos := reader; pos < sets; pos += stride {
			idx, slot := pos/scaleSetsPerObject, pos%scaleSetsPerObject

			set, route := run.Route(scaleKey(idx, slot))
			if !assert.Equal(t, flagruntime.RouteFlagSet, route) {
				return checked
			}

			res, err := eval.IntVariationDetails(set, "flag-000", ctx, -1)
			if err != nil {
				assert.Fail(t, "the evaluation failed", err.Error())

				return checked
			}

			if !assert.False(t, res.Failed) {
				return checked
			}

			assert.Equal(t, scaleValue(run.Generation, idx), res.Value, "another generation than the runtime")

			checked++
		}

		return checked
	}

	// The pinned reader loads generation 1, waits until generation 2 is published, and only then evaluates what it
	// loaded: the runtime it holds must still answer with generation 1. The gate is opened by the cleanup too, so a
	// failed test cannot leave the reader parked.
	loaded, published := make(chan struct{}), make(chan struct{})
	publish := sync.OnceFunc(func() { close(published) })

	t.Cleanup(publish)

	wg.Go(func() {
		run := harness.holder.Load()

		close(loaded)
		<-published

		assert.Equal(t, uint64(1), run.Generation)
		assert.Greater(t, harness.holder.Load().Generation, run.Generation, "the swap was published before the pass")
		pinned.Add(pass(run, 0))
	})

	for reader := 1; reader < readers; reader++ {
		wg.Go(func() {
			// at least one pass, whatever the scheduler does
			for first := true; first || !stop.Load(); first = false {
				evaluated.Add(pass(harness.holder.Load(), reader))
			}
		})
	}

	expired, stopTimer := bound()
	defer stopTimer()

	select {
	case <-loaded:
	case <-expired:
		require.FailNow(t, "the pinned reader did not load a runtime")
	}

	for gen := uint64(2); gen <= generations; gen++ {
		res := harness.feed(scaleGeneration(t, gen, sets))
		assert.Equal(t, flagruntime.OutcomeApplied, res.Outcome)

		publish()
	}

	stop.Store(true)
	wg.Wait()

	assert.Positive(t, evaluated.Load())
	assert.EqualValues(t, len(scalePositions(sets, stride, 0)), pinned.Load(), "the pinned reader checked all")
	assert.Equal(t, uint64(generations), harness.holder.Load().Generation)
	assert.Empty(t, mixedSets(harness.holder.Load(), generations, sets))
}

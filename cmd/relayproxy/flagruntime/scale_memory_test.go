package flagruntime_test

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
)

// serialEnv is set by the memory test: setting it forbids t.Parallel, so the test runs alone.
const serialEnv = "SEMAPHORE_SCALE_MEMORY_SERIAL"

// liveHeap is the live heap after a collection: HeapAlloc counts the objects that are not yet swept, so the
// collection runs twice (the second one frees what the finalisers and the first sweep left).
func liveHeap() uint64 {
	var stats runtime.MemStats

	runtime.GC()
	runtime.GC()
	runtime.ReadMemStats(&stats)

	return stats.HeapAlloc
}

// growth is after - before, floored at zero (the heap can shrink between two measures).
func growth(after, before uint64) uint64 {
	if after < before {
		return 0
	}

	return after - before
}

// TestScaleMemoryDuringSwap measures the live heap of a fleet (scaleSets flag sets of scaleFlagsPerSet flags):
//
//   - one generation held is the baseline B (measured at 1.4 MB of runtime before it, 5.9 MB at 500 flag sets, 58 MB
//     at 5000);
//   - a full-generation swap while a reader still holds the previous runtime holds two versions: at most 2 x B
//     (measured 1.54 to 1.61 x: the two generations share the engine's tables and the shared flag definitions);
//   - once the reader lets go, the heap is back to about the baseline: at most 1.3 x B (measured 1.08 x at the full
//     size, 1.19 x at 500 sets: the engine keeps a few fixed structures of the last round);
//   - a one-object change (ten flag sets) while a reader holds the previous runtime adds the rebuilt flag sets plus
//     the name tables of the new runtime (a few words per unchanged flag set, the map copies): at most 2 x the
//     share of the rebuilt flag sets plus 256 B per flag set of the fleet, and at most 5% of B (measured 1.3% at
//     the full size, 2.4% at 500). It never costs a second generation.
//
// The heap is deterministic after a collection (the spread of the figures over repeated runs is below 0.1%), so
// the margins above are model margins, not noise margins. The test reads the heap of the whole process, which a
// parallel test would pollute, so it runs alone (the parallel tests of the package are paused until every serial
// test ended). t.Setenv makes that explicit: a test that sets an environment variable cannot call t.Parallel.
// Every figure is logged.
func TestScaleMemoryDuringSwap(t *testing.T) {
	const (
		fullSwapFactor   = 2.0
		releasedFactor   = 1.3
		tableBytesPerSet = 256
		partialShareMax  = 0.05
	)

	t.Setenv(serialEnv, "1")

	harness := newRig(t, flagruntime.Hooks{})
	harness.start()

	empty := liveHeap()

	require.Equal(t, flagruntime.OutcomeApplied, harness.feed(scaleGeneration(t, 1, scaleSets)).Outcome)

	one := growth(liveHeap(), empty)
	require.NotZero(t, one)

	// a reader that holds generation 1 while generation 2 replaces every flag set
	twoHeld := heldSwap(t, harness, scaleGeneration(t, 2, scaleSets), 1, empty)

	assert.LessOrEqual(t, float64(twoHeld), fullSwapFactor*float64(one), "two generations at most during the swap")
	assert.Greater(t, float64(twoHeld), 1.3*float64(one), "the measure sees both generations (the reader held one)")

	// the reader let go
	released := growth(liveHeap(), empty)

	assert.LessOrEqual(t, float64(released), releasedFactor*float64(one), "back to one generation")

	// one object changed (ten flag sets of the fleet) while a reader holds the previous runtime
	before := liveHeap()
	partial := growth(heldSwap(t, harness, partialContent(t, 3), 2, 0), before)
	share := float64(one) * float64(scaleSetsPerObject) / float64(scaleSets)

	assert.LessOrEqual(t, float64(partial), 2*share+tableBytesPerSet*scaleSets, "the rebuilt sets and the tables")
	assert.LessOrEqual(t, float64(partial), partialShareMax*float64(one), "far from a second generation")

	t.Logf("fleet %d sets x %d flags: empty %d B, one generation %d B, swap with reader %d B (%.2f x), "+
		"released %d B (%.2f x), one object change %d B (%.2f x the share %.0f B)",
		scaleSets, scaleFlagsPerSet, empty, one, twoHeld, float64(twoHeld)/float64(one),
		released, float64(released)/float64(one), partial, float64(partial)/share, share)
}

// partialContent is generation 3 of the fleet where only the object idx changed from generation 2.
func partialContent(tb testing.TB, idx int) *flagruntime.Content {
	tb.Helper()

	return scaleFleet(tb, 3, scaleSets, func(obj int) int {
		if obj == idx {
			return scaleValue(3, obj)
		}

		return scaleValue(2, obj)
	})
}

// heldSwap loads the runtime (a reader that holds it), feeds the content and returns the live heap above base while
// the reader still holds the old runtime. The reader lets go when the function returns.
func heldSwap(
	t *testing.T, harness *rig, content *flagruntime.Content, wantHeldGeneration uint64, base uint64,
) uint64 {
	t.Helper()

	reader := harness.holder.Load()

	require.Equal(t, flagruntime.OutcomeApplied, harness.feed(content).Outcome)

	heap := growth(liveHeap(), base)

	assert.Equal(t, wantHeldGeneration, reader.Generation, "the reader still holds its generation")
	assert.Empty(t, mixedSets(reader, wantHeldGeneration, scaleSets), "and it is intact")

	return heap
}

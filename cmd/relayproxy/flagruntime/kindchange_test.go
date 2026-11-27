package flagruntime_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
)

// A name that changes kind between two generations is first reported with the kind of its fallback bytes while the
// new object is failing, and later with its new kind, at the same generation. That is a state change of the object,
// not another manifest: the engine applies it, and the new document is served without a third generation.
func TestKindChange_RecoversAtTheSameGeneration(t *testing.T) {
	t.Parallel()

	harness := newRig(t, flagruntime.Hooks{})
	harness.start()

	flags := flagsObj(1, "f")
	doc := setsObj(t, fset("z", src(flagsName), "key-z"))
	moved := setsObj(t, fset("moved", src(flagsName), "key-moved"))

	// generation 1: sharedName is a flags object
	first := objs{flagsName: flags, sharedName: flags, setsName: doc}
	require.Equal(t, flagruntime.OutcomeApplied, harness.feed(content(1, first)).Outcome)

	// generation 2, first snapshot: the manifest lists sharedName as a document, its fetch failed, so it is stale and
	// still carries the flags (kind flags)
	stalled := first.with(objs{sharedName: stale(&flags)})
	require.Equal(t, flagruntime.OutcomeApplied, harness.feed(content(2, stalled)).Outcome)

	// generation 2, second snapshot: the document arrived, with its own kind
	res := harness.feed(content(2, first.with(objs{sharedName: moved})))
	assert.Equal(t, flagruntime.OutcomeApplied, res.Outcome, res.Detail)
	assert.Equal(t, flagruntime.CodeApplied, res.Code)

	run := harness.holder.Load()

	for _, key := range []string{"key-z", "key-moved"} {
		_, ok := routedSet(run, key)
		assert.True(t, ok, key)
	}
}

// Two objects that both carry their current content, with other kinds at the same generation, are a conflict: the
// manifest of that generation is not the one that runs.
func TestKindChange_BothCurrentIsAConflict(t *testing.T) {
	t.Parallel()

	harness := newRig(t, flagruntime.Hooks{})
	harness.start()

	doc := setsObj(t, fset("z", src(flagsName), "key-z"))
	moved := setsObj(t, fset("moved", src(flagsName), "key-moved"))

	require.Equal(
		t,
		flagruntime.OutcomeApplied,
		harness.feed(
			content(1, objs{flagsName: flagsObj(1, "f"), sharedName: flagsObj(1, "g"), setsName: doc}),
		).Outcome,
	)

	res := harness.feed(content(1, objs{flagsName: flagsObj(1, "f"), sharedName: moved, setsName: doc}))

	assert.Equal(t, flagruntime.OutcomeRejected, res.Outcome)
	assert.Equal(t, flagruntime.CodeGenerationConflict, res.Code)
}

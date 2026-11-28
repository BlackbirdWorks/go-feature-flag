package flagruntime_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

const (
	betaFlagYAML = `
beta-flag:
  variations:
    on: true
    off: false
  targeting:
    - name: members
      query: targetingKey in segment("beta")
      variation: on
  defaultRule:
    variation: off
staff-flag:
  variations:
    on: true
    off: false
  targeting:
    - name: staff
      query: targetingKey in segment("staff")
      variation: on
  defaultRule:
    variation: off
`
	peopleBeta = "apiVersion: semaphore/v1\nkind: Segments\nsegments:\n  - name: beta\n    members: [alice]\n"
)

func segmentRig(t *testing.T) (*flagruntime.Builder, *flagruntime.Evaluator, objs) {
	t.Helper()

	svc := newService(t)
	builder := flagruntime.NewBuilder(svc, flagruntime.BuilderOptions{})
	eval := flagruntime.NewEvaluator(svc, nil)
	all := objs{
		"f1":      obj(flagruntime.KindFlags, []byte(betaFlagYAML)),
		"f2":      flagsObj(1, "z"),
		segObject: segObj(moreSegments),
		setsName:  withSegmentSources(t),
	}

	return builder, eval, all
}

func evalBeta(t *testing.T, eval *flagruntime.Evaluator, set *flagruntime.FlagSet, key string) (bool, segment.Status) {
	t.Helper()

	got, err := eval.BoolVariationDetails(set, "beta-flag", ffcontext.NewEvaluationContext(key), false)
	require.NoError(t, err)

	return got.Value, got.SegmentStatus
}

// The catalog of a flag set is made from its segment sources: static members are answered from memory, a dynamic
// segment is defined and has no provider yet, and a flag set without segment sources has no catalog.
func TestAFlagSetHasTheCatalogOfItsSegmentSources(t *testing.T) {
	t.Parallel()

	builder, eval, all := segmentRig(t)
	built := buildAndCommit(t, builder, content(1, all))
	setA, setB := built.Runtime.FlagSets["a"], built.Runtime.FlagSets["b"]

	require.NotNil(t, setA.Segments)
	assert.Nil(t, setB.Segments, "no segment source, no catalog")
	assert.Nil(t, setB.SegmentCatalog())
	assert.Equal(t, 1, setA.Segments.Static)
	assert.Equal(t, 1, setA.Segments.Dynamic)
	assert.Equal(t, &flagruntime.SegmentCounts{Static: 1, Dynamic: 1}, built.Runtime.Status.FlagSets["a"].Segments)
	assert.Nil(t, built.Runtime.Status.FlagSets["b"].Segments)

	tests := []struct {
		name       string
		key        string
		wantValue  bool
		wantStatus segment.Status
	}{
		{name: "a member of the static segment", key: "alice", wantValue: true, wantStatus: segment.StatusHealthy},
		{name: "another member", key: "bob", wantValue: true, wantStatus: segment.StatusHealthy},
		{name: "not a member", key: "carol", wantValue: false, wantStatus: segment.StatusHealthy},
	}

	// moreSegments has alice and bob in beta
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			value, status := evalBeta(t, eval, setA, tt.key)
			assert.Equal(t, tt.wantValue, value)
			assert.Equal(t, tt.wantStatus, status)
		})
	}

	got, err := eval.BoolVariationDetails(setA, "staff-flag", ffcontext.NewEvaluationContext("alice"), false)
	require.NoError(t, err)
	assert.False(t, got.Value, "a dynamic segment with no provider answers by the default")
	assert.Equal(t, segment.StatusNotConfigured, got.SegmentStatus)
}

// A reload that changes the members swaps the catalog of the flag set at once: a reader that holds the old flag
// set keeps the old members, the new flag set has the new ones; a flag set that does not depend on the change is
// carried over by pointer.
func TestAReloadThatChangesTheMembersSwapsTheCatalogAtomically(t *testing.T) {
	t.Parallel()

	builder, eval, all := segmentRig(t)
	all[segObject] = segObj(peopleBeta)
	first := buildAndCommit(t, builder, content(1, all))
	oldA, oldB := first.Runtime.FlagSets["a"], first.Runtime.FlagSets["b"]

	value, _ := evalBeta(t, eval, oldA, "bob")
	assert.False(t, value, "bob is not in the first version")

	second := buildAndCommit(t, builder, content(2, all.with(objs{segObject: segObj(moreSegments)})))
	newA := second.Runtime.FlagSets["a"]

	assert.NotSame(t, oldA, newA, "the flag set that lists the object is rebuilt")
	assert.Same(t, oldB, second.Runtime.FlagSets["b"], "a flag set that does not depend on it is carried over")

	value, _ = evalBeta(t, eval, newA, "bob")
	assert.True(t, value, "bob is in the second version")

	value, _ = evalBeta(t, eval, oldA, "bob")
	assert.False(t, value, "a reader that holds the old flag set sees the old members")

	state, static := oldA.SegmentCatalog().Member("beta", "bob")
	assert.True(t, static)
	assert.Equal(t, segment.NotMember, state)
}

// A flags object that changes does not rebuild the flag sets that only differ by segments, and a segments object
// that does not change keeps the catalog by pointer.
func TestACatalogIsKeptWhileItsSegmentsObjectIsUnchanged(t *testing.T) {
	t.Parallel()

	builder, _, all := segmentRig(t)
	first := buildAndCommit(t, builder, content(1, all))
	second := buildAndCommit(t, builder, content(2, all.with(objs{"f2": flagsObj(2, "z")})))

	assert.Same(t, first.Runtime.FlagSets["a"], second.Runtime.FlagSets["a"])
	assert.Same(t, first.Runtime.FlagSets["a"].Segments, second.Runtime.FlagSets["a"].Segments)
}

// A segment source that is not a loaded segments object degrades the flag set; the flag set is still served.
func TestAMissingSegmentSourceDegradesTheFlagSet(t *testing.T) {
	t.Parallel()

	builder, _, all := segmentRig(t)
	delete(all, segObject)

	built := buildAndCommit(t, builder, content(1, all))
	status := built.Runtime.Status.FlagSets["a"]

	require.NotNil(t, status)
	assert.NotEmpty(t, status.Problems)
}

// The same swap on a full pass (a new object name makes the builder validate everything again): the flag set that
// lists the changed segments object is rebuilt, the other one is carried over.
func TestAChangedSegmentsObjectRebuildsItsFlagSetOnAFullPass(t *testing.T) {
	t.Parallel()

	builder, eval, all := segmentRig(t)
	all[segObject] = segObj(peopleBeta)
	first := buildAndCommit(t, builder, content(1, all))

	second := buildAndCommit(t, builder, content(2, all.with(objs{
		segObject: segObj(moreSegments),
		"f3":      flagsObj(1, "extra"), // a new name: a full pass
	})))

	assert.NotSame(t, first.Runtime.FlagSets["a"], second.Runtime.FlagSets["a"])
	assert.Same(t, first.Runtime.FlagSets["b"], second.Runtime.FlagSets["b"])

	value, _ := evalBeta(t, eval, second.Runtime.FlagSets["a"], "bob")
	assert.True(t, value)
}

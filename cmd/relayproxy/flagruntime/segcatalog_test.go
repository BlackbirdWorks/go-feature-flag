package flagruntime_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

const (
	memberAlice  = "alice"
	memberBob    = "bob"
	segOneName   = "seg-one"
	betaFlagName = "beta-flag"
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

	got, err := eval.BoolVariationDetails(set, betaFlagName, ffcontext.NewEvaluationContext(key), false)
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
		{name: "a member of the static segment", key: memberAlice, wantValue: true, wantStatus: segment.StatusHealthy},
		{name: "another member", key: memberBob, wantValue: true, wantStatus: segment.StatusHealthy},
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

	got, err := eval.BoolVariationDetails(setA, "staff-flag", ffcontext.NewEvaluationContext(memberAlice), false)
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

	value, _ := evalBeta(t, eval, oldA, memberBob)
	assert.False(t, value, "bob is not in the first version")

	second := buildAndCommit(t, builder, content(2, all.with(objs{segObject: segObj(moreSegments)})))
	newA := second.Runtime.FlagSets["a"]

	assert.NotSame(t, oldA, newA, "the flag set that lists the object is rebuilt")
	assert.Same(t, oldB, second.Runtime.FlagSets["b"], "a flag set that does not depend on it is carried over")

	value, _ = evalBeta(t, eval, newA, memberBob)
	assert.True(t, value, "bob is in the second version")

	value, _ = evalBeta(t, eval, oldA, memberBob)
	assert.False(t, value, "a reader that holds the old flag set sees the old members")

	state, static := oldA.SegmentCatalog().Member("beta", memberBob)
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

	value, _ := evalBeta(t, eval, second.Runtime.FlagSets["a"], memberBob)
	assert.True(t, value)
}

const (
	segTwo      = "second"
	peopleTwo   = "apiVersion: semaphore/v1\nkind: Segments\nsegments:\n  - name: other\n    members: [dave]\n"
	peopleBeta2 = "apiVersion: semaphore/v1\nkind: Segments\nsegments:\n  - name: beta\n    members: [alice, bob, carol]\n"
)

func twoSegmentSources(t *testing.T) flagruntime.Object {
	t.Helper()

	return setsObj(
		t,
		controlplane.FlagSet{
			Name: "a", Sources: src("f1"), SegmentSources: []string{segObject, segTwo}, APIKeys: []string{keyA},
		},
	)
}

// A flag set is built only when every one of its sources resolves: while a segment source was never good the set is
// pending and serves nothing, whatever the other source does; a set that was built keeps its flags and its catalog
// together. The incremental pass (same names) and the full pass (a new name) give the same result.
func TestAFlagSetWithAnUnresolvedSegmentSourceIsNotBuiltByAChangeOfAnother(t *testing.T) {
	t.Parallel()

	tests := []struct {
		first        func(objs) objs
		second       func(objs) objs
		name         string
		wantFlags    int
		wantSegments bool
	}{
		{
			name:  "never built: the other source changes",
			first: func(in objs) objs { return in.with(objs{segTwo: segObj(garbage)}) },
			second: func(in objs) objs {
				return in.with(objs{segObject: segObj(moreSegments)})
			},
		},
		{
			name:  "built, then a source goes away: the set keeps its build whole",
			first: func(in objs) objs { return in.with(objs{segTwo: segObj(peopleTwo)}) },
			second: func(in objs) objs {
				return in.without(segTwo).with(objs{segObject: segObj(moreSegments)})
			},
			wantFlags: 2, wantSegments: true,
		},
	}

	for _, tt := range tests {
		for _, full := range []bool{false, true} {
			name := tt.name + ", incremental"
			if full {
				name = tt.name + ", full pass"
			}

			t.Run(name, func(t *testing.T) {
				t.Parallel()

				builder, _, all := segmentRig(t)
				all[segObject] = segObj(peopleBeta)
				all[setsName] = twoSegmentSources(t)
				all = tt.first(all)
				buildAndCommit(t, builder, content(1, all))

				next := tt.second(all)
				if full {
					next = next.with(objs{"f9": flagsObj(1, "extra")})
				}

				built := buildAndCommit(t, builder, content(2, next))
				set := built.Runtime.FlagSets["a"]
				require.NotNil(t, set)

				assert.Len(t, set.Flags, tt.wantFlags, "flags served")
				assert.Equal(t, tt.wantSegments, set.Segments != nil, "catalog of the build")

				if tt.wantSegments {
					state, _ := set.SegmentCatalog().Member("beta", memberBob)
					assert.Equal(t, segment.NotMember, state, "the catalog is the one of the build of the flags")
				}
			})
		}
	}
}

const (
	otherFlagYAML = `
other-flag:
  variations:
    on: true
    off: false
  targeting:
    - name: members
      query: targetingKey in segment("beta")
      variation: on
  defaultRule:
    variation: off
`
	peopleBob = "apiVersion: semaphore/v1\nkind: Segments\nsegments:\n  - name: beta\n    members: [bob]\n"
)

// The public flag set keeps, for each flag, the catalog of the flag set that it comes from: with one public flag set
// the static segments work for the keyless requests, and with two public flag sets that define the same segment name
// with other members a flag is targeted by its own set's members, never by a merged namespace.
func TestThePublicFlagSetEvaluatesEachFlagAgainstTheCatalogOfItsOwnFlagSet(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantByFlag map[string]map[string]bool // flag, key: value
		name       string
		sets       []controlplane.FlagSet
	}{
		{
			name: "one public flag set",
			sets: []controlplane.FlagSet{
				{Name: "pub-one", Sources: src("f1"), SegmentSources: []string{segOneName}},
			},
			wantByFlag: map[string]map[string]bool{betaFlagName: {memberAlice: true, memberBob: false}},
		},
		{
			name: "two public flag sets, the same segment name, other members",
			sets: []controlplane.FlagSet{
				{Name: "pub-one", Sources: src("f1"), SegmentSources: []string{segOneName}},
				{Name: "pub-two", Sources: src("f2"), SegmentSources: []string{"seg-two"}},
			},
			wantByFlag: map[string]map[string]bool{
				betaFlagName: {memberAlice: true, memberBob: false},
				"other-flag": {memberAlice: false, memberBob: true},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := newService(t)
			builder := flagruntime.NewBuilder(svc, flagruntime.BuilderOptions{})
			eval := flagruntime.NewEvaluator(svc, nil)
			built := buildAndCommit(t, builder, content(1, objs{
				"f1":       obj(flagruntime.KindFlags, []byte(betaFlagYAML)),
				"f2":       obj(flagruntime.KindFlags, []byte(otherFlagYAML)),
				segOneName: segObj(peopleBeta),
				"seg-two":  segObj(peopleBob),
				setsName:   setsObj(t, tt.sets...),
			}))

			public := built.Runtime.Public
			require.NotNil(t, public)

			for flag, byKey := range tt.wantByFlag {
				for key, want := range byKey {
					got, err := eval.BoolVariationDetails(public, flag, ffcontext.NewEvaluationContext(key), false)
					require.NoError(t, err)
					assert.Equal(t, want, got.Value, "%s for %s", flag, key)
				}
			}

			states := eval.AllFlagsState(public, ffcontext.NewEvaluationContext(memberBob))
			for flag, byKey := range tt.wantByFlag {
				assert.Equal(t, byKey[memberBob], states.GetFlags()[flag].Value, "all flags: %s", flag)
			}
		})
	}
}

const (
	segHeader        = "apiVersion: semaphore/v1\nkind: Segments\nsegments:\n"
	docBetaUnused    = segHeader + "  - name: beta\n    members: [alice]\n  - name: unused\n    members: [x]\n"
	docBetaUnusedTwo = segHeader + "  - name: beta\n    members: [alice]\n  - name: unused\n    members: [x, y]\n"
	docBetaTwo       = segHeader + "  - name: beta\n    members: [alice, bob]\n  - name: unused\n    members: [x]\n"
)

// A publish that only changes the members of a segment changes the evaluations of the flags that use it: they are
// reported as updated (and no other flag is), and the hashes of /v1/flag/change change; a segment that no flag uses
// changes nothing.
func TestAMembershipOnlyChangeIsAChangeOfTheFlagsThatUseTheSegment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		after        string
		wantUpdated  []string
		wantEvents   int
		wantHashDiff bool
	}{
		{
			name: "a segment that a flag uses", after: docBetaTwo, wantUpdated: []string{betaFlagName},
			wantEvents: 1, wantHashDiff: true,
		},
		{name: "a segment that no flag uses", after: docBetaUnusedTwo},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			harness := newRig(t, flagruntime.Hooks{})
			harness.start()

			all := objs{
				"f1":      obj(flagruntime.KindFlags, []byte(betaFlagYAML)),
				"f2":      flagsObj(1, "z"),
				segObject: segObj(docBetaUnused),
				setsName:  withSegmentSources(t),
			}

			first := harness.feed(content(1, all))
			for range first.Events {
				harness.nextChange()
			}

			before, err := harness.holder.Load().FlagSets["a"].Change()
			require.NoError(t, err)

			second := harness.feed(content(2, all.with(objs{segObject: segObj(tt.after)})))
			assert.Equal(t, tt.wantEvents, second.Events, "flag sets with a change to tell")

			if tt.wantEvents > 0 {
				change := harness.nextChange()
				assert.Equal(t, "a", change.FlagSet)
				assert.ElementsMatch(t, tt.wantUpdated, mapKeys(change.Diff.Updated))
				assert.Empty(t, change.Diff.Added)
				assert.Empty(t, change.Diff.Deleted)
			}

			after, err := harness.holder.Load().FlagSets["a"].Change()
			require.NoError(t, err)

			assert.Equal(t, tt.wantHashDiff, before.Hash != after.Hash, "the hash of the flag set")
			assert.Equal(
				t,
				tt.wantHashDiff,
				before.Flags[betaFlagName] != after.Flags[betaFlagName],
				"the hash of the flag",
			)
			assert.Equal(t, before.Flags["staff-flag"], after.Flags["staff-flag"], "a flag that uses another segment")
		})
	}
}

// A segments object that stops being good keeps serving its last good members: the evaluations stay HEALTHY (static
// members are published content like flags, and a kept flag is not marked in a response either) and the degradation
// is where the flags report theirs: the status of the object (state, held checksum and reason) and its flag sets.
func TestARetainedSegmentsObjectIsReportedInTheStatusNotInTheEvaluation(t *testing.T) {
	t.Parallel()

	builder, eval, all := segmentRig(t)
	all[segObject] = segObj(peopleBeta)
	good := buildAndCommit(t, builder, content(1, all))

	bad := buildAndCommit(t, builder, content(2, all.with(objs{segObject: segObj(garbage)})))
	set := bad.Runtime.FlagSets["a"]

	value, status := evalBeta(t, eval, set, memberAlice)
	assert.True(t, value, "the last good members still answer")
	assert.Equal(t, segment.StatusHealthy, status)

	held := bad.Runtime.Status.Objects[segObject]
	require.NotNil(t, held)
	assert.Equal(t, flagruntime.ObjectInvalid, held.State)
	assert.Equal(
		t,
		good.Runtime.Status.Objects[segObject].Checksum,
		held.Checksum,
		"the checksum of the content that serves",
	)
	assert.NotEmpty(t, held.Reason)
}

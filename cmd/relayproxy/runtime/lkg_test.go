package runtime_test

import (
	"context"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/runtime"
	"github.com/blackbirdworks/semaphore/controlplane"
)

// lkgCase is one scenario of the last-known-good table of issue 6: the contents are built in order and the
// want fields describe the last runtime. A nil want map or slice is checked as "nothing expected".
type lkgCase struct {
	wantStates   map[string]runtime.SetState
	wantFlags    map[string][]string
	wantValues   map[string]any
	wantObjects  map[string]runtime.ObjectState
	wantKeys     map[string]string
	wantRejected map[string]int
	name         string
	steps        []objs
	wantAbsent   []string
	wantSame     []string
	wantNotSame  []string
	wantEntries  []controlplane.Code
}

// lkgFixtures are the objects the cases build on.
type lkgFixtures struct {
	oneSet  objs
	twoSets objs
	first   runtime.Object
	second  runtime.Object
}

func newLKGFixtures(t *testing.T) *lkgFixtures {
	t.Helper()

	first := flagsObj(1, "x", "y")

	return &lkgFixtures{
		first:   first,
		second:  flagsObj(2, "x"),
		oneSet:  objs{"f1": first, setsName: setsObj(t, fset("a", src("f1"), keyA))},
		twoSets: objs{"f1": first, setsName: setsObj(t, fset("a", src("f1"), keyA), fset("b", src("f1"), keyB))},
	}
}

func lkgCases(t *testing.T) []lkgCase {
	t.Helper()

	return slices.Concat(lkgFlagCases(t), lkgDocumentCases(t), lkgEntryCases(t))
}

func lkgFlagCases(t *testing.T) []lkgCase {
	t.Helper()

	fix := newLKGFixtures(t)

	return []lkgCase{
		{
			name: "invalid flag keeps its previous definition, the others update",
			steps: []objs{
				fix.oneSet,
				fix.oneSet.with(objs{"f1": obj(runtime.KindFlags, join(flagsYAML(2, "x"), badYAML("y")))}),
			},
			wantStates:   map[string]runtime.SetState{"a": runtime.StateDegraded},
			wantFlags:    map[string][]string{"a": {"x", "y"}},
			wantValues:   map[string]any{pathAX: 2, pathAY: 1},
			wantObjects:  map[string]runtime.ObjectState{"f1": runtime.ObjectOK},
			wantRejected: map[string]int{"a": 1},
			wantKeys:     map[string]string{keyA: "a"},
			wantSame:     nil,
			wantNotSame:  []string{pathAX},
		},
		{
			name: "invalid flag with no previous definition is absent and counted",
			steps: []objs{
				fix.oneSet.with(objs{"f1": obj(runtime.KindFlags, join(flagsYAML(1, "x"), badYAML("y")))}),
			},
			wantStates:   map[string]runtime.SetState{"a": runtime.StateDegraded},
			wantFlags:    map[string][]string{"a": {"x"}},
			wantValues:   map[string]any{pathAX: 1},
			wantObjects:  map[string]runtime.ObjectState{"f1": runtime.ObjectOK},
			wantRejected: map[string]int{"a": 1},
			wantKeys:     map[string]string{keyA: "a"},
		},
		{
			name: "stale object keeps its last good table",
			steps: []objs{
				fix.oneSet,
				fix.oneSet.with(
					objs{"f1": {Kind: runtime.KindFlags, State: runtime.ObjectStale, Reason: "etag unchanged"}},
				),
			},
			wantStates:  map[string]runtime.SetState{"a": runtime.StateDegraded},
			wantFlags:   map[string][]string{"a": {"x", "y"}},
			wantValues:  map[string]any{pathAX: 1, pathAY: 1},
			wantObjects: map[string]runtime.ObjectState{"f1": runtime.ObjectStale},
			wantKeys:    map[string]string{keyA: "a"},
			wantSame:    []string{pathAX, pathAY},
		},
		{
			name: "missing object after a good load keeps its last good table",
			steps: []objs{
				fix.oneSet,
				fix.oneSet.with(
					objs{"f1": {Kind: runtime.KindFlags, State: runtime.ObjectMissing, Reason: "not found"}},
				),
			},
			wantStates:  map[string]runtime.SetState{"a": runtime.StateDegraded},
			wantFlags:   map[string][]string{"a": {"x", "y"}},
			wantValues:  map[string]any{pathAX: 1},
			wantObjects: map[string]runtime.ObjectState{"f1": runtime.ObjectMissing},
			wantKeys:    map[string]string{keyA: "a"},
			wantSame:    []string{pathAX},
		},
		{
			name: "stale then ok again with the same content serves again and keeps the pointers",
			steps: []objs{
				fix.oneSet,
				fix.oneSet.with(objs{"f1": {Kind: runtime.KindFlags, State: runtime.ObjectStale}}),
				fix.oneSet,
			},
			wantStates:  map[string]runtime.SetState{"a": runtime.StateServing},
			wantFlags:   map[string][]string{"a": {"x", "y"}},
			wantValues:  map[string]any{pathAX: 1},
			wantObjects: map[string]runtime.ObjectState{"f1": runtime.ObjectOK},
			wantKeys:    map[string]string{keyA: "a"},
			wantSame:    []string{pathAX, pathAY},
		},
		{
			name:        "missing object on cold start: the flag set is pending with zero flags",
			steps:       []objs{fix.oneSet.with(objs{"f1": {Kind: runtime.KindFlags, State: runtime.ObjectMissing}})},
			wantStates:  map[string]runtime.SetState{"a": runtime.StatePending},
			wantFlags:   map[string][]string{"a": {}},
			wantObjects: map[string]runtime.ObjectState{"f1": runtime.ObjectMissing},
			wantKeys:    map[string]string{keyA: "a"},
		},
		{
			name:        "pending object on cold start: the flag set is pending",
			steps:       []objs{fix.oneSet.with(objs{"f1": {Kind: runtime.KindFlags, State: runtime.ObjectPending}})},
			wantStates:  map[string]runtime.SetState{"a": runtime.StatePending},
			wantFlags:   map[string][]string{"a": {}},
			wantObjects: map[string]runtime.ObjectState{"f1": runtime.ObjectPending},
			wantKeys:    map[string]string{keyA: "a"},
		},
		{
			name: "undecodable flags object keeps the last good table",
			steps: []objs{
				fix.oneSet,
				fix.oneSet.with(objs{"f1": obj(runtime.KindFlags, []byte("x: [unterminated"))}),
			},
			wantStates:  map[string]runtime.SetState{"a": runtime.StateDegraded},
			wantFlags:   map[string][]string{"a": {"x", "y"}},
			wantValues:  map[string]any{pathAX: 1},
			wantObjects: map[string]runtime.ObjectState{"f1": runtime.ObjectInvalid},
			wantSame:    []string{pathAX},
			wantKeys:    map[string]string{keyA: "a"},
		},
		{
			name:        "undecodable flags object on cold start leaves the flag set pending",
			steps:       []objs{fix.oneSet.with(objs{"f1": obj(runtime.KindFlags, []byte("x: [unterminated"))})},
			wantStates:  map[string]runtime.SetState{"a": runtime.StatePending},
			wantFlags:   map[string][]string{"a": {}},
			wantObjects: map[string]runtime.ObjectState{"f1": runtime.ObjectInvalid},
			wantKeys:    map[string]string{keyA: "a"},
		},
	}
}

func lkgDocumentCases(t *testing.T) []lkgCase {
	t.Helper()

	fix := newLKGFixtures(t)

	return []lkgCase{
		{
			name:        "withdrawn flag set: its key still routes to it and it serves nothing",
			steps:       []objs{fix.twoSets, fix.twoSets.with(objs{setsName: setsObj(t, fset("b", src("f1"), keyB))})},
			wantStates:  map[string]runtime.SetState{"a": runtime.StateWithdrawn, "b": runtime.StateServing},
			wantFlags:   map[string][]string{"a": {}, "b": {"x", "y"}},
			wantKeys:    map[string]string{keyA: "a", keyB: "b"},
			wantObjects: map[string]runtime.ObjectState{"f1": runtime.ObjectOK, setsName: runtime.ObjectOK},
			wantNotSame: []string{pathAX},
		},
		{
			name: "a withdrawn flag set that returns serves again",
			steps: []objs{
				fix.twoSets,
				fix.twoSets.with(objs{setsName: setsObj(t, fset("b", src("f1"), keyB))}),
				fix.twoSets,
			},
			wantStates: map[string]runtime.SetState{"a": runtime.StateServing, "b": runtime.StateServing},
			wantFlags:  map[string][]string{"a": {"x", "y"}},
			wantKeys:   map[string]string{keyA: "a", keyB: "b"},
			wantSame:   []string{pathBX},
		},
		{
			name: "a flag-set document removed from the content withdraws its flag sets",
			steps: []objs{
				{
					"f1":  fix.first,
					docs1: setsObj(t, fset("a", src("f1"), keyA)),
					docs2: setsObj(t, fset("b", src("f1"), keyB)),
				},
				{"f1": fix.first, docs1: setsObj(t, fset("a", src("f1"), keyA))},
			},
			wantStates: map[string]runtime.SetState{"a": runtime.StateServing, "b": runtime.StateWithdrawn},
			wantFlags:  map[string][]string{"a": {"x", "y"}, "b": {}},
			wantKeys:   map[string]string{keyA: "a", keyB: "b"},
			wantSame:   []string{pathAX},
		},
		{
			name: "undecodable flag-set document keeps the previous flag sets",
			steps: []objs{
				fix.twoSets,
				fix.twoSets.with(objs{setsName: obj(runtime.KindFlagSets, []byte("flagSets: [unterminated"))}),
			},
			wantStates:  map[string]runtime.SetState{"a": runtime.StateServing, "b": runtime.StateServing},
			wantFlags:   map[string][]string{"a": {"x", "y"}, "b": {"x", "y"}},
			wantKeys:    map[string]string{keyA: "a", keyB: "b"},
			wantObjects: map[string]runtime.ObjectState{setsName: runtime.ObjectInvalid},
			wantSame:    []string{pathAX},
		},
		{
			name: "stale flag-set document keeps the previous flag sets",
			steps: []objs{
				fix.twoSets,
				fix.twoSets.with(objs{setsName: {Kind: runtime.KindFlagSets, State: runtime.ObjectStale}}),
			},
			wantStates:  map[string]runtime.SetState{"a": runtime.StateServing, "b": runtime.StateServing},
			wantKeys:    map[string]string{keyA: "a", keyB: "b"},
			wantObjects: map[string]runtime.ObjectState{setsName: runtime.ObjectStale},
		},
		{
			name:       "an empty flag-set document is a valid empty runtime",
			steps:      []objs{{setsName: setsObj(t)}},
			wantStates: map[string]runtime.SetState{},
			wantAbsent: []string{"a"},
		},
	}
}

func lkgEntryCases(t *testing.T) []lkgCase {
	t.Helper()

	fix := newLKGFixtures(t)

	return []lkgCase{
		{
			name: "invalid entry keeps the last valid definition of its flag set",
			steps: []objs{
				fix.twoSets,
				fix.twoSets.with(objs{setsName: setsObj(t, fset("a", nil, keyA), fset("b", src("f1"), keyB))}),
			},
			wantStates:  map[string]runtime.SetState{"a": runtime.StateDegraded, "b": runtime.StateServing},
			wantFlags:   map[string][]string{"a": {"x", "y"}, "b": {"x", "y"}},
			wantKeys:    map[string]string{keyA: "a", keyB: "b"},
			wantEntries: []controlplane.Code{controlplane.CodeMissingValue},
		},
		{
			name: "invalid entry with no previous definition: absent and pending, the others load",
			steps: []objs{
				fix.twoSets.with(objs{setsName: setsObj(t, fset("a", nil, keyA), fset("b", src("f1"), keyB))}),
			},
			wantStates:  map[string]runtime.SetState{"a": runtime.StatePending, "b": runtime.StateServing},
			wantFlags:   map[string][]string{"b": {"x", "y"}},
			wantAbsent:  []string{"a"},
			wantKeys:    map[string]string{keyB: "b"},
			wantEntries: []controlplane.Code{controlplane.CodeMissingValue},
		},
		{
			name: "unresolved source with no previous definition is reported, the others load",
			steps: []objs{
				fix.twoSets.with(objs{setsName: setsObj(t, fset("a", src("nope"), keyA), fset("b", src("f1"), keyB))}),
			},
			wantStates:  map[string]runtime.SetState{"a": runtime.StatePending, "b": runtime.StateServing},
			wantFlags:   map[string][]string{"b": {"x", "y"}},
			wantAbsent:  []string{"a"},
			wantKeys:    map[string]string{keyB: "b"},
			wantEntries: []controlplane.Code{controlplane.CodeUnresolvedSource},
		},
		{
			name: "unresolved source after a good definition keeps the definition",
			steps: []objs{
				fix.twoSets,
				fix.twoSets.with(objs{setsName: setsObj(t, fset("a", src("nope"), keyA), fset("b", src("f1"), keyB))}),
			},
			wantStates:  map[string]runtime.SetState{"a": runtime.StateDegraded, "b": runtime.StateServing},
			wantFlags:   map[string][]string{"a": {"x", "y"}},
			wantKeys:    map[string]string{keyA: "a", keyB: "b"},
			wantEntries: []controlplane.Code{controlplane.CodeUnresolvedSource},
			wantSame:    []string{pathBX},
		},
		{
			name: "an object removed from the content leaves the flag sets that list it on their last definition",
			steps: []objs{
				objs{"f1": fix.first, "f2": fix.second, setsName: setsObj(t, fset("a", src("f1", "f2"), keyA))},
				objs{"f1": fix.first, setsName: setsObj(t, fset("a", src("f1", "f2"), keyA))},
			},
			wantStates:  map[string]runtime.SetState{"a": runtime.StateDegraded},
			wantFlags:   map[string][]string{"a": {"x", "y"}},
			wantValues:  map[string]any{pathAX: 1},
			wantKeys:    map[string]string{keyA: "a"},
			wantEntries: []controlplane.Code{controlplane.CodeUnresolvedSource},
		},
		{
			name: "duplicate key in one document: the first flag set holds it, the other is reported",
			steps: []objs{fix.twoSets.with(objs{setsName: setsObj(t,
				fset("a", src("f1"), sharedName, keyA), fset("b", src("f1"), sharedName, keyB))})},
			wantStates:  map[string]runtime.SetState{"a": runtime.StateServing, "b": runtime.StateDegraded},
			wantFlags:   map[string][]string{"a": {"x", "y"}, "b": {"x", "y"}},
			wantKeys:    map[string]string{sharedName: "a", keyA: "a", keyB: "b"},
			wantEntries: []controlplane.Code{controlplane.CodeDuplicateKey},
		},
		{
			name: "duplicate key across documents: the flag set that was accepted first keeps it",
			steps: []objs{
				objs{"f1": fix.first, docs2: setsObj(t, fset("b", src("f1"), sharedName, keyB))},
				objs{
					"f1": fix.first, docs1: setsObj(t, fset("a", src("f1"), sharedName, keyA)),
					docs2: setsObj(t, fset("b", src("f1"), sharedName, keyB)),
				},
			},
			wantStates:  map[string]runtime.SetState{"a": runtime.StateDegraded, "b": runtime.StateServing},
			wantFlags:   map[string][]string{"a": {"x", "y"}, "b": {"x", "y"}},
			wantKeys:    map[string]string{sharedName: "b", keyA: "a", keyB: "b"},
			wantEntries: []controlplane.Code{controlplane.CodeDuplicateKey},
			wantSame:    []string{pathBX},
		},
		{
			name: "duplicate flag set name across documents: the first entry holds the name",
			steps: []objs{objs{
				"f1": fix.first, docs1: setsObj(t, fset("a", src("f1"), keyA)),
				docs2: setsObj(t, fset("a", src("f1"), "key-other")),
			}},
			wantStates:  map[string]runtime.SetState{"a": runtime.StateServing},
			wantFlags:   map[string][]string{"a": {"x", "y"}},
			wantKeys:    map[string]string{keyA: "a"},
			wantEntries: []controlplane.Code{controlplane.CodeDuplicateName},
		},
		{
			name: "duplicate flag across sources: the later source wins",
			steps: []objs{objs{
				"f1": fix.first, "f2": fix.second, setsName: setsObj(t, fset("a", src("f1", "f2"), keyA)),
			}},
			wantStates: map[string]runtime.SetState{"a": runtime.StateServing},
			wantFlags:  map[string][]string{"a": {"x", "y"}},
			wantValues: map[string]any{pathAX: 2, pathAY: 1},
			wantKeys:   map[string]string{keyA: "a"},
		},
		{
			name: "duplicate flag across sources: reversed order reverses the winner",
			steps: []objs{objs{
				"f1": fix.first, "f2": fix.second, setsName: setsObj(t, fset("a", src("f2", "f1"), keyA)),
			}},
			wantStates: map[string]runtime.SetState{"a": runtime.StateServing},
			wantFlags:  map[string][]string{"a": {"x", "y"}},
			wantValues: map[string]any{pathAX: 1, pathAY: 1},
			wantKeys:   map[string]string{keyA: "a"},
		},
	}
}

func TestBuilderLastKnownGood(t *testing.T) {
	t.Parallel()

	for _, tt := range lkgCases(t) {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			b := runtime.NewBuilder(newService(t), runtime.BuilderOptions{})

			var prev, last *runtime.Runtime

			for i, step := range tt.steps {
				prev = last
				last = buildAndCommit(t, b, content(uint64(i+1), step)).Runtime
			}

			assertLKG(t, &tt, prev, last)
		})
	}
}

func assertLKG(t *testing.T, tt *lkgCase, prev, last *runtime.Runtime) {
	t.Helper()

	for name, want := range tt.wantStates {
		stat := last.Status.FlagSets[name]
		require.NotNil(t, stat, "status of %s", name)
		assert.Equal(t, want, stat.State, "state of %s", name)

		if fs, ok := last.FlagSetByName(name); ok {
			assert.Equal(t, want, fs.State, "runtime state of %s", name)
		}
	}

	for name, want := range tt.wantFlags {
		fs, ok := last.FlagSetByName(name)
		require.True(t, ok, "flag set %s is in the runtime", name)
		assert.Equal(t, want, append([]string{}, flagNames(fs)...), "flags of %s", name)
	}

	for _, name := range tt.wantAbsent {
		_, ok := last.FlagSetByName(name)
		assert.False(t, ok, "flag set %s is not in the runtime", name)
	}

	for path, want := range tt.wantValues {
		var set, flagName string

		for i := range path {
			if path[i] == '/' {
				set, flagName = path[:i], path[i+1:]
			}
		}

		fs, ok := last.FlagSetByName(set)
		require.True(t, ok, set)
		assert.EqualValues(t, want, value(fs, flagName), "value of %s", path)
	}

	for name, want := range tt.wantObjects {
		stat := last.Status.Objects[name]
		require.NotNil(t, stat, "status of object %s", name)
		assert.Equal(t, want, stat.State, "state of object %s", name)
	}

	assert.Len(t, last.ByKey, len(tt.wantKeys), "the routed keys")

	for key, want := range tt.wantKeys {
		fs, ok := last.FlagSetByKey(key)
		require.True(t, ok, "key %s routes", key)
		assert.Equal(t, want, fs.Name, "owner of key %s", key)
	}

	for name, want := range tt.wantRejected {
		assert.Equal(t, want, last.FlagSets[name].Rejected, "rejected flags of %s", name)
		assert.Equal(t, want, last.Status.FlagSets[name].Rejected, "rejected flags of %s in the status", name)
	}

	codes := make([]controlplane.Code, 0, len(last.Status.Entries))
	for _, e := range last.Status.Entries {
		codes = append(codes, e.Code)
	}

	if len(tt.wantEntries) == 0 {
		assert.Empty(t, codes, "entry problems")
	} else {
		assert.Equal(t, tt.wantEntries, codes, "entry problems")
	}

	assertPointers(t, tt.wantSame, prev, last, true)
	assertPointers(t, tt.wantNotSame, prev, last, false)
}

func assertPointers(t *testing.T, paths []string, prev, last *runtime.Runtime, same bool) {
	t.Helper()

	for _, path := range paths {
		var set, flagName string

		for i := range path {
			if path[i] == '/' {
				set, flagName = path[:i], path[i+1:]
			}
		}

		require.NotNil(t, prev, "a previous runtime is needed for %s", path)

		oldSet, _ := prev.FlagSetByName(set)
		newSet, _ := last.FlagSetByName(set)
		oldFlag, oldOK := oldSet.Flag(flagName)
		newFlag, newOK := newSet.Flag(flagName)

		require.True(t, oldOK, "%s in the previous runtime", path)

		if same {
			require.True(t, newOK, "%s in the runtime", path)
			assert.Same(t, oldFlag, newFlag, "%s keeps its pointer", path)

			continue
		}

		assert.False(t, newOK && oldFlag == newFlag, "%s must not keep its pointer", path)
	}
}

func TestBuilderStatusOfAnInvalidFlag(t *testing.T) {
	t.Parallel()

	builder := runtime.NewBuilder(newService(t), runtime.BuilderOptions{})
	objects := objs{
		"f1":     flagsObj(1, "x", "y"),
		setsName: setsObj(t, fset("a", src("f1"), keyA)),
	}
	buildAndCommit(t, builder, content(1, objects))

	changed := obj(runtime.KindFlags, join(flagsYAML(2, "x"), badYAML("y")))
	built := buildAndCommit(t, builder, content(2, objects.with(objs{"f1": changed})))

	status := built.Runtime.Status.Objects["f1"]
	require.NotNil(t, status)
	require.Len(t, status.Rejected, 1)
	assert.Equal(t, "y", status.Rejected[0].Flag)
	assert.True(t, status.Rejected[0].Kept, "the flag keeps its previous definition")
	assert.NotEmpty(t, status.Rejected[0].Reason)
	assert.Equal(t, 2, status.Flags)
	assert.Equal(t, runtime.ObjectOK, status.State)
	assert.Equal(t, controlplane.ChecksumOf(changed.Bytes).Value, status.Checksum, "the checksum is the new content")
}

func TestBuilderHashIdentifiesContent(t *testing.T) {
	t.Parallel()

	b := runtime.NewBuilder(newService(t), runtime.BuilderOptions{})
	objects := objs{
		"f1": flagsObj(1, "x"),
		"f2": flagsObj(2, "x"),
		setsName: setsObj(
			t,
			fset("a", src("f1")),
			fset("same", src("f1")),
			fset("other", src("f2")),
			fset("both", src("f1", "f2")),
		),
	}
	built, err := b.Build(context.Background(), content(1, objects))
	require.NoError(t, err)

	sets := built.Runtime.FlagSets

	assert.Equal(t, sets["a"].Hash, sets["same"].Hash, "the same sources give the same hash")
	assert.NotEqual(t, sets["a"].Hash, sets["other"].Hash, "another source gives another hash")
	assert.NotEqual(t, sets["a"].Hash, sets["both"].Hash, "more sources give another hash")
	assert.NotZero(t, sets["a"].Hash)
}

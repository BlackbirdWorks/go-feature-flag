package flagruntime_test

import (
	"maps"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/controlplane/flagcheck"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
)

const (
	segObject    = "people"
	goodSegments = "apiVersion: semaphore/v1\nkind: Segments\nsegments:\n  - name: beta\n    members: [alice]\n"
	moreSegments = "apiVersion: semaphore/v1\nkind: Segments\nsegments:\n  - name: beta\n    members: [alice, bob]\n" +
		"  - name: staff\n    connection: audiences\n"
	badSegments = "apiVersion: semaphore/v1\nkind: Segments\nsegments:\n  - name: beta\n    members: [alice, alice]\n"
	garbage     = "apiVersion: semaphore/v1\nkind: Segments\nsegments: [\n"
)

func segObj(body string) flagruntime.Object { return obj(flagruntime.KindSegments, []byte(body)) }

// withSegmentSources is a flag-set document of two flag sets: a lists the segments object, b does not.
func withSegmentSources(t *testing.T) flagruntime.Object {
	t.Helper()

	return setsObj(
		t,
		controlplane.FlagSet{
			Name:           "a",
			Sources:        src("f1"),
			SegmentSources: []string{segObject},
			APIKeys:        []string{keyA},
		},
		controlplane.FlagSet{Name: "b", Sources: src("f2"), APIKeys: []string{keyB}},
	)
}

func TestASegmentsObjectIsAcceptedAndReported(t *testing.T) {
	t.Parallel()

	builder := flagruntime.NewBuilder(newService(t), flagruntime.BuilderOptions{})
	all := objs{
		"f1":      flagsObj(1, "x"),
		"f2":      flagsObj(1, "z"),
		segObject: segObj(goodSegments),
		setsName:  withSegmentSources(t),
	}

	built := buildAndCommit(t, builder, content(1, all))

	assert.ElementsMatch(
		t,
		[]string{"a", "b"},
		mapKeys(built.Runtime.FlagSets),
		"the flag set that lists the segments object is served",
	)
	assert.Empty(t, built.Runtime.Status.Entries, "the segment source resolves")

	status := built.Runtime.Status.Objects[segObject]
	require.NotNil(t, status)
	assert.Equal(t, flagruntime.ObjectStatus{
		Name: segObject, Kind: flagruntime.KindSegments, State: flagruntime.ObjectOK,
		Checksum: controlplane.ChecksumOf([]byte(goodSegments)).Value, Flags: 1,
	}, *status, "the object shows in the status with its checksum and the number of segments")
}

func TestABadSegmentsObjectKeepsItsLastGoodContentAndTheFlagSets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		bad       string
		wantState flagruntime.ObjectState
	}{
		{name: "it does not decode", bad: garbage, wantState: flagruntime.ObjectInvalid},
		{name: "it does not validate", bad: badSegments, wantState: flagruntime.ObjectInvalid},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			builder := flagruntime.NewBuilder(newService(t), flagruntime.BuilderOptions{})
			good := objs{
				"f1": flagsObj(
					1,
					"x",
				),
				"f2":      flagsObj(1, "z"),
				segObject: segObj(goodSegments),
				setsName:  withSegmentSources(t),
			}
			buildAndCommit(t, builder, content(1, good))

			built := buildAndCommit(t, builder, content(2, good.with(objs{segObject: segObj(tt.bad)})))

			status := built.Runtime.Status.Objects[segObject]
			require.NotNil(t, status)
			assert.Equal(t, tt.wantState, status.State)
			assert.Equal(
				t,
				controlplane.ChecksumOf([]byte(goodSegments)).Value,
				status.Checksum,
				"the last good content is kept",
			)
			assert.NotEmpty(t, status.Reason)
			assert.ElementsMatch(
				t,
				[]string{"a", "b"},
				mapKeys(built.Runtime.FlagSets),
				"both flag sets keep being served",
			)
			assert.Empty(t, built.Runtime.Status.Entries)
		})
	}
}

func TestASegmentsObjectThatWasNeverGoodDegradesOnlyTheFlagSetsThatListIt(t *testing.T) {
	t.Parallel()

	builder := flagruntime.NewBuilder(newService(t), flagruntime.BuilderOptions{})
	all := objs{
		"f1":      flagsObj(1, "x"),
		"f2":      flagsObj(1, "z"),
		segObject: segObj(garbage),
		setsName:  withSegmentSources(t),
	}

	built := buildAndCommit(t, builder, content(1, all))

	require.NotNil(t, built.Runtime.FlagSets["b"], "the flag set that does not list it is served")
	assert.Len(t, built.Runtime.FlagSets["b"].Flags, 1)
	assert.Equal(t, flagruntime.ObjectInvalid, built.Runtime.Status.Objects[segObject].State)

	var unresolved []flagruntime.EntryProblem

	for _, entry := range built.Runtime.Status.Entries {
		if entry.Code == controlplane.CodeUnresolvedSource {
			unresolved = append(unresolved, entry)
		}
	}

	require.Len(t, unresolved, 1, "the flag set that lists it has an invalid entry")
	assert.Contains(t, unresolved[0].Reason, `"people" is not an object of kind segments`)

	// the object becomes good: the flag set is built from its sources
	built = buildAndCommit(t, builder, content(2, all.with(objs{segObject: segObj(moreSegments)})))
	assert.ElementsMatch(t, []string{"a", "b"}, mapKeys(built.Runtime.FlagSets))
	assert.Empty(t, built.Runtime.Status.Entries, "it resolves now")
	assert.Equal(t, flagruntime.ObjectOK, built.Runtime.Status.Objects[segObject].State)
	assert.Equal(t, 2, built.Runtime.Status.Objects[segObject].Flags)

	// the object goes away: the name no longer resolves, the flag set keeps the flags it had
	gone := objs{"f1": all["f1"], "f2": all["f2"], setsName: all[setsName]}
	built = buildAndCommit(t, builder, content(3, gone))
	assert.NotContains(t, built.Runtime.Status.Objects, segObject)
	assert.NotNil(t, built.Runtime.FlagSets["a"], "the flag set keeps the flags it had")
}

func TestASwapOfSegmentsObjectsDropsTheOldOneAndDegradesTheFlagSetsThatListIt(t *testing.T) {
	t.Parallel()

	builder := flagruntime.NewBuilder(newService(t), flagruntime.BuilderOptions{})
	first := objs{
		"f1": flagsObj(
			1,
			"x",
		),
		"f2":      flagsObj(1, "z"),
		segObject: segObj(goodSegments),
		setsName:  withSegmentSources(t),
	}

	built := buildAndCommit(t, builder, content(1, first))
	require.NotNil(t, built.Runtime.Status.Objects[segObject])

	// one object goes and another one comes: the number of segments objects does not change
	swapped := objs{
		"f1": flagsObj(2, "x"), "f2": first["f2"], "other-people": segObj(goodSegments), setsName: first[setsName],
	}

	built = buildAndCommit(t, builder, content(2, swapped))

	assert.NotContains(
		t,
		built.Runtime.Status.Objects,
		segObject,
		"the object that the manifest no longer lists is dropped",
	)
	assert.Contains(t, built.Runtime.Status.Objects, "other-people")

	var unresolved []flagruntime.EntryProblem

	for _, entry := range built.Runtime.Status.Entries {
		if entry.Code == controlplane.CodeUnresolvedSource {
			unresolved = append(unresolved, entry)
		}
	}

	require.Len(t, unresolved, 1, "the flag set that lists the removed name is unresolved")
	assert.Contains(t, unresolved[0].Reason, `"people" is not an object of kind segments`)

	// it keeps the flags it had: the new value of f1 is not served to it
	flag := built.Runtime.FlagSets["a"].Flags["x"]
	require.NotNil(t, flag)
	assert.Contains(t, mustVariations(flag), "v", "the flag set keeps its last good flags")
	assert.EqualValues(t, 1, variationValue(flag), "the content of the last good generation, not the new f1")
	assert.EqualValues(
		t,
		1,
		variationValue(built.Runtime.FlagSets["b"].Flags["z"]),
		"the other flag set is not affected",
	)
}

func mustVariations(flag *flagdef.InternalFlag) map[string]*any { return flag.GetVariations() }

func variationValue(flag *flagdef.InternalFlag) any {
	return *flag.GetVariations()["v"]
}

// TestTheCheckOfTheCliPicksTheFlagsThatTheRelayServes keeps flagcheck.Winners (the effective flags of the reference
// check) and the merge of the relay together: for every flag key, the object that the check names is the one whose
// definition the relay serves.
func TestTheCheckOfTheCliPicksTheFlagsThatTheRelayServes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		sources []string
	}{
		{name: "in order", sources: []string{"f1", "f2", "f3"}},
		{name: "reversed", sources: []string{"f3", "f2", "f1"}},
		{name: "one source", sources: []string{"f2"}},
	}

	// f1 defines x, y; f2 defines y, z; f3 defines z, w: each object has its own value, to tell the winner
	files := map[string]flagruntime.Object{
		"f1": flagsObj(1, "x", "y"), "f2": flagsObj(2, "y", "z"), "f3": flagsObj(3, "z", "w"),
	}
	values := map[string]int{"f1": 1, "f2": 2, "f3": 3}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			builder := flagruntime.NewBuilder(newService(t), flagruntime.BuilderOptions{})
			published := objs{
				setsName: setsObj(t, controlplane.FlagSet{Name: "s", Sources: tt.sources, APIKeys: []string{keyA}}),
			}
			maps.Copy(published, files)

			built := buildAndCommit(t, builder, content(1, published))

			var checked []flagcheck.FlagFile

			for _, name := range tt.sources {
				checked = append(checked, flagcheck.FlagFile{Name: name, Format: "yaml", Body: files[name].Bytes})
			}

			winners := flagcheck.Winners(checked)
			require.NotEmpty(t, winners)

			for key, object := range winners {
				served := built.Runtime.FlagSets["s"].Flags[key]
				require.NotNil(t, served, key)
				assert.EqualValues(
					t,
					values[object],
					variationValue(served),
					"the flag %s: the relay serves the object %s",
					key,
					object,
				)
			}

			assert.Len(t, built.Runtime.FlagSets["s"].Flags, len(winners))
		})
	}
}

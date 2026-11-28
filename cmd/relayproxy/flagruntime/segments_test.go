package flagruntime_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/controlplane"
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

	assert.Equal(
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
			assert.Equal(t, []string{"a", "b"}, mapKeys(built.Runtime.FlagSets), "both flag sets keep being served")
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
	assert.Equal(t, []string{"a", "b"}, mapKeys(built.Runtime.FlagSets))
	assert.Empty(t, built.Runtime.Status.Entries, "it resolves now")
	assert.Equal(t, flagruntime.ObjectOK, built.Runtime.Status.Objects[segObject].State)
	assert.Equal(t, 2, built.Runtime.Status.Objects[segObject].Flags)

	// the object goes away: the name no longer resolves, the flag set keeps the flags it had
	gone := objs{"f1": all["f1"], "f2": all["f2"], setsName: all[setsName]}
	built = buildAndCommit(t, builder, content(3, gone))
	assert.NotContains(t, built.Runtime.Status.Objects, segObject)
	assert.NotNil(t, built.Runtime.FlagSets["a"], "the flag set keeps the flags it had")
}

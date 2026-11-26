package flagruntime_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/controlplane"
)

const jsonFlagX = `{"x": {"variations": {"v": 1}, "defaultRule": {"variation": "v"}}}`

func hashOfA(t *testing.T, objects objs) uint64 {
	t.Helper()

	built := buildAndCommit(t, flagruntime.NewBuilder(newService(t), flagruntime.BuilderOptions{}), content(1, objects))

	fset, ok := built.Runtime.FlagSetByName("a")
	require.True(t, ok)

	return fset.Hash
}

func TestFlagSetHashFollowsTheEvaluableContent(t *testing.T) {
	t.Parallel()

	const ruleFlag = "x:\n  variations:\n    v: 1\n    w: 2\n" +
		"  targeting:\n    - query: key eq \"a\"\n      variation: w\n" +
		"  defaultRule:\n    variation: v\n"

	base := objs{"f1": flagsObj(1, "x"), setsName: setsObj(t, fset("a", src("f1"), keyA))}
	want := hashOfA(t, base)

	tests := []struct {
		objects  objs
		name     string
		wantSame bool
	}{
		{
			name: "a comment-only edit keeps the hash",
			objects: base.with(
				objs{"f1": obj(flagruntime.KindFlags, append([]byte("# a comment\n"), flagsYAML(1, "x")...))},
			),
			wantSame: true,
		},
		{
			name: "the same flag from a json object has the same hash",
			objects: base.with(objs{"f1": {
				Kind: flagruntime.KindFlags, Format: controlplane.FormatJSON, Bytes: []byte(jsonFlagX),
				Checksum: "json-checksum",
			}}),
			wantSame: true,
		},
		{
			name:     "another set name does not matter",
			objects:  objs{"f1": flagsObj(1, "x"), setsName: setsObj(t, fset("a", src("f1"), "other-key"))},
			wantSame: true,
		},
		{name: "a changed value changes the hash", objects: base.with(objs{"f1": flagsObj(2, "x")})},
		{name: "a renamed flag changes the hash", objects: base.with(objs{"f1": flagsObj(1, "y")})},
		{name: "an added flag changes the hash", objects: base.with(objs{"f1": flagsObj(1, "x", "z")})},
		{name: "a rule changes the hash", objects: base.with(objs{"f1": obj(flagruntime.KindFlags, []byte(ruleFlag))})},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := hashOfA(t, tt.objects)

			if tt.wantSame {
				assert.Equal(t, want, got)
			} else {
				assert.NotEqual(t, want, got)
			}
		})
	}
}

func TestFlagSetHashIgnoresTheOrderOfDisjointSources(t *testing.T) {
	t.Parallel()

	built := buildAndCommit(t, flagruntime.NewBuilder(newService(t), flagruntime.BuilderOptions{}), content(1, objs{
		"f1": flagsObj(1, "x"), "f2": flagsObj(2, "y"),
		setsName: setsObj(t, fset("ab", src("f1", "f2")), fset("ba", src("f2", "f1")), fset("one", src("f1"))),
	}))

	sets := built.Runtime.FlagSets

	assert.Equal(t, sets["ab"].Hash, sets["ba"].Hash)
	assert.NotEqual(t, sets["ab"].Hash, sets["one"].Hash)
}

func TestFlagSetHashIsThatOfTheServedFlags(t *testing.T) {
	t.Parallel()

	builder := flagruntime.NewBuilder(newService(t), flagruntime.BuilderOptions{})
	base := objs{"f1": flagsObj(1, "x", "y"), setsName: setsObj(t, fset("a", src("f1"), keyA))}

	buildAndCommit(t, builder, content(1, base))

	// x is updated, y is invalid and keeps its previous definition
	changed := obj(flagruntime.KindFlags, join(flagsYAML(2, "x"), badYAML()))
	built := buildAndCommit(t, builder, content(2, base.with(objs{"f1": changed})))

	served := hashOfA(t, objs{
		"f1":     obj(flagruntime.KindFlags, join(flagsYAML(2, "x"), flagsYAML(1, "y"))),
		setsName: base[setsName],
	})

	assert.Equal(t, served, built.Runtime.FlagSets["a"].Hash, "the hash is that of what is served, not of the bytes")
	assert.NotEqual(t, hashOfA(t, base), served)
}

func TestCommentOnlyEditEmitsNoEvent(t *testing.T) {
	t.Parallel()

	harness := newRig(t, flagruntime.Hooks{})
	harness.start()

	base := objs{"f1": flagsObj(1, "x"), setsName: setsObj(t, fset("a", src("f1"), keyA))}

	harness.feed(content(1, base))
	harness.nextChange()

	first := harness.holder.Load().FlagSets["a"]

	harness.feed(
		content(2, base.with(objs{"f1": obj(flagruntime.KindFlags, append([]byte("# edit\n"), flagsYAML(1, "x")...))})),
	)

	second := harness.holder.Load().FlagSets["a"]

	assert.NotSame(t, first, second, "the flag set is rebuilt")
	assert.Equal(t, first.Hash, second.Hash)
	assert.Same(t, first.Flags["x"], second.Flags["x"], "the flag keeps its pointer")

	// a real change comes next: it is the first event, so the comment-only round queued none
	harness.feed(content(3, base.with(objs{"f1": flagsObj(2, "x")})))

	change := harness.nextChange()

	assert.Equal(t, uint64(3), change.Generation)
	assert.Empty(t, harness.eng.Changes())
}

// A flag that has no JSON encoding (a NaN in its metadata) is hashed from its object and key, so two versions
// that differ only in such a value do not hash alike.
func TestFlagSetHashSeesChangesOfFlagsWithoutAJSONEncoding(t *testing.T) {
	t.Parallel()

	nanFlag := func(n int) flagruntime.Object {
		return obj(flagruntime.KindFlags, fmt.Appendf(nil,
			"x:\n  variations:\n    v: 1\n  defaultRule:\n    variation: v\n  metadata:\n    m: .nan\n    n: %d\n", n))
	}

	sets := setsObj(t, fset("a", src("f1"), keyA))
	first := hashOfA(t, objs{"f1": nanFlag(1), setsName: sets})

	assert.NotEqual(t, first, hashOfA(t, objs{"f1": nanFlag(2), setsName: sets}))
	assert.Equal(t, first, hashOfA(t, objs{"f1": nanFlag(1), setsName: sets}), "the same bytes hash alike")

	harness := newRig(t, flagruntime.Hooks{})
	harness.start()

	harness.feed(content(1, objs{"f1": nanFlag(1), setsName: sets}))
	harness.nextChange()

	set := harness.holder.Load().FlagSets["a"]

	require.Contains(t, set.Flags, "x", "the flag is valid and served")

	harness.feed(content(2, objs{"f1": nanFlag(2), setsName: sets}))

	assert.Contains(t, harness.nextChange().Diff.Updated, "x")
}

package flagruntime_test

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/controlplane"
)

// buildAndCommit builds a content and commits it, as the engine does after the store.
func buildAndCommit(
	t *testing.T, b *flagruntime.Builder, c *flagruntime.Content,
) *flagruntime.Built {
	t.Helper()

	built, err := b.Build(context.Background(), c)
	require.NoError(t, err)

	b.Commit(built)

	return built
}

func rebuiltNames(built *flagruntime.Built) []string {
	out := make([]string, 0, len(built.Rebuilt))
	for _, rb := range built.Rebuilt {
		out = append(out, rb.Name)
	}

	return out
}

// baseObjs is four flag sets over three flags objects: a and b use f1, c and d use f3, b also uses f2.
func baseObjs(t *testing.T) objs {
	t.Helper()

	return objs{
		"f1": flagsObj(1, "x", "y"),
		"f2": flagsObj(1, "z"),
		"f3": flagsObj(1, "p", "q"),
		setsName: setsObj(t,
			fset("a", src("f1"), keyA),
			fset("b", src("f1", "f2"), keyB),
			fset("c", src("f3"), "key-c"),
			fset("d", src("f3"), "key-d"),
		),
	}
}

func TestBuilderStructuralSharing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		change          func(t *testing.T, base objs) objs
		name            string
		wantRebuilt     []string
		wantParsed      int
		wantReused      int
		wantCachedAfter int
		wantKeys        int
	}{
		{
			name:            "one flags object changes: only the flag sets that list it are rebuilt",
			change:          func(_ *testing.T, base objs) objs { return base.with(objs{"f1": flagsObj(2, "x", "y")}) },
			wantRebuilt:     []string{"a", "b"},
			wantParsed:      1,
			wantReused:      2,
			wantCachedAfter: 4,
			wantKeys:        4,
		},
		{
			name:            "a flags object listed by one flag set only",
			change:          func(_ *testing.T, base objs) objs { return base.with(objs{"f2": flagsObj(2, "z")}) },
			wantRebuilt:     []string{"b"},
			wantParsed:      1,
			wantReused:      3,
			wantCachedAfter: 4,
			wantKeys:        4,
		},
		{
			name:            "a new generation with the same objects rebuilds nothing and parses nothing",
			change:          func(_ *testing.T, base objs) objs { return base.with(nil) },
			wantRebuilt:     []string{},
			wantParsed:      0,
			wantReused:      4,
			wantCachedAfter: 4,
			wantKeys:        4,
		},
		{
			name: "a flag-set document changes: only the changed entry is rebuilt",
			change: func(t *testing.T, base objs) objs {
				t.Helper()

				return base.with(objs{setsName: setsObj(t,
					fset("a", src("f1"), keyA),
					fset("b", src("f1", "f2"), keyB),
					fset("c", src("f3"), "key-c", "key-c2"),
					fset("d", src("f3"), "key-d"),
				)})
			},
			wantRebuilt:     []string{"c"},
			wantParsed:      1,
			wantReused:      3,
			wantCachedAfter: 4,
			wantKeys:        5,
		},
		{
			name: "an object that no flag set lists is parsed and rebuilds nothing",
			change: func(_ *testing.T, base objs) objs {
				return base.with(objs{"unused": flagsObj(1, "u")})
			},
			wantRebuilt:     []string{},
			wantParsed:      1,
			wantReused:      4,
			wantCachedAfter: 5,
			wantKeys:        4,
		},
		{
			name: "removing an object drops its parse result",
			change: func(_ *testing.T, base objs) objs {
				return base.with(objs{"unused": flagsObj(1, "u")}).without("f2")
			},
			wantRebuilt:     []string{"b"},
			wantParsed:      1,
			wantReused:      3,
			wantCachedAfter: 4,
			wantKeys:        4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var parsed []flagruntime.ParseEvent

			b := flagruntime.NewBuilder(newService(t), flagruntime.BuilderOptions{
				OnParse: func(e flagruntime.ParseEvent) { parsed = append(parsed, e) },
			})
			base := baseObjs(t)
			first := buildAndCommit(t, b, content(1, base))

			assert.Equal(t, 4, first.Stats.Rebuilt, "first build creates every flag set")

			parsed = nil

			second := buildAndCommit(t, b, content(2, tt.change(t, base)))

			assert.Equal(t, tt.wantRebuilt, rebuiltNames(second))
			assert.Equal(t, tt.wantParsed, second.Stats.Parsed)
			assert.Len(t, parsed, tt.wantParsed)
			assert.Equal(t, tt.wantReused, second.Stats.Reused)
			assert.Equal(t, len(tt.wantRebuilt), second.Stats.Rebuilt)
			assert.Equal(t, tt.wantCachedAfter, b.CachedParses())

			for name, old := range first.Runtime.FlagSets {
				got := second.Runtime.FlagSets[name]
				if slices.Contains(tt.wantRebuilt, name) {
					assert.NotSame(t, old, got, "flag set %s must be rebuilt", name)

					continue
				}

				assert.Same(t, old, got, "flag set %s must be carried over by pointer", name)
			}

			assert.Len(t, second.Runtime.FlagSets, 4)
			assert.Len(t, second.Runtime.ByKey, tt.wantKeys)

			for key, fs := range second.Runtime.ByKey {
				assert.Same(
					t,
					second.Runtime.FlagSets[fs.Name],
					fs,
					"key %s routes to the flag set of the runtime",
					key,
				)
			}
		})
	}
}

func TestBuilderSharesFlagsBetweenFlagSets(t *testing.T) {
	t.Parallel()

	const sets = 100

	flagSets := make([]controlplane.FlagSet, 0, sets)
	for i := range sets {
		flagSets = append(flagSets, fset("set-"+string(rune('a'+i/26))+string(rune('a'+i%26)), src(sharedName)))
	}

	var parsed []flagruntime.ParseEvent

	b := flagruntime.NewBuilder(newService(t), flagruntime.BuilderOptions{
		OnParse: func(e flagruntime.ParseEvent) { parsed = append(parsed, e) },
	})
	built := buildAndCommit(t, b, content(1, objs{
		sharedName: flagsObj(1, "x", "y", "z"), setsName: setsObj(t, flagSets...),
	}))

	var sharedParses int

	for _, e := range parsed {
		if e.Kind == flagruntime.KindFlags {
			sharedParses++
		}
	}

	assert.Equal(t, 1, sharedParses, "a flags object listed by 100 flag sets is parsed once")
	require.Len(t, built.Runtime.FlagSets, sets)

	first := built.Runtime.FlagSets["set-aa"]
	require.NotNil(t, first)

	for name, fs := range built.Runtime.FlagSets {
		for _, flagName := range []string{"x", "y", "z"} {
			want, _ := first.Flag(flagName)
			got, ok := fs.Flag(flagName)
			assert.True(t, ok, "%s has flag %s", name, flagName)
			assert.Same(t, want, got, "%s shares flag %s by pointer", name, flagName)
		}
	}
}

func TestBuilderCostIsProportionalToTheChange(t *testing.T) {
	t.Parallel()

	const (
		objectCount   = 20
		setsPerObject = 10
	)

	objects := objs{}
	flagSets := make([]controlplane.FlagSet, 0, objectCount*setsPerObject)

	for o := range objectCount {
		name := "obj-" + string(rune('a'+o))
		objects[name] = flagsObj(o+1, "f1", "f2", "f3")

		for s := range setsPerObject {
			flagSets = append(flagSets, fset("set-"+name+"-"+string(rune('a'+s)), src(name)))
		}
	}

	objects[setsName] = setsObj(t, flagSets...)

	b := flagruntime.NewBuilder(newService(t), flagruntime.BuilderOptions{})
	first := buildAndCommit(t, b, content(1, objects))

	require.Equal(t, objectCount*setsPerObject, first.Stats.Rebuilt)
	require.Equal(t, objectCount+1, first.Stats.Parsed)

	second := buildAndCommit(t, b, content(2, objects.with(objs{"obj-c": flagsObj(100, "f1", "f2", "f3")})))

	assert.Equal(t, 1, second.Stats.Parsed, "one changed object is parsed once")
	assert.Equal(t, setsPerObject, second.Stats.Rebuilt, "only the flag sets that list it are rebuilt")
	assert.Equal(t, objectCount*setsPerObject-setsPerObject, second.Stats.Reused)

	third := buildAndCommit(t, b, content(3, objects.with(objs{"obj-c": flagsObj(100, "f1", "f2", "f3")})))

	assert.Equal(t, 0, third.Stats.Parsed, "a no-op generation parses nothing")
	assert.Equal(t, 0, third.Stats.Rebuilt, "a no-op generation rebuilds nothing")
}

func TestBuilderKeepsUnchangedFlagsByPointerWithinAChangedObject(t *testing.T) {
	t.Parallel()

	b := flagruntime.NewBuilder(newService(t), flagruntime.BuilderOptions{})
	objects := objs{
		"f1":     obj(flagruntime.KindFlags, join(flagsYAML(1, "same"), flagsYAML(1, "moves"))),
		setsName: setsObj(t, fset("a", src("f1"), keyA)),
	}
	first := buildAndCommit(t, b, content(1, objects))

	changed := join(flagsYAML(1, "same"), flagsYAML(2, "moves", "new"))
	second := buildAndCommit(t, b, content(2, objects.with(objs{"f1": obj(flagruntime.KindFlags, changed)})))

	oldSet, newSet := first.Runtime.FlagSets["a"], second.Runtime.FlagSets["a"]

	sameOld, _ := oldSet.Flag("same")
	sameNew, _ := newSet.Flag("same")
	movesOld, _ := oldSet.Flag("moves")
	movesNew, _ := newSet.Flag("moves")

	assert.Same(t, sameOld, sameNew, "a flag with the same definition keeps its pointer")
	assert.NotSame(t, movesOld, movesNew, "a flag with a new definition gets a new pointer")
	assert.Equal(t, []string{"moves", "new", "same"}, flagNames(newSet))
}

func TestBuilderEmptyContent(t *testing.T) {
	t.Parallel()

	b := flagruntime.NewBuilder(newService(t), flagruntime.BuilderOptions{})
	built := buildAndCommit(t, b, content(1, objs{}))

	assert.Empty(t, built.Runtime.FlagSets)
	assert.Empty(t, built.Runtime.ByKey)
	assert.Equal(t, uint64(1), built.Runtime.Generation)
	assert.Equal(t, 0, built.Stats.Parsed)
}

func TestBuilderRejectsNilContentAndHonoursContext(t *testing.T) {
	t.Parallel()

	b := flagruntime.NewBuilder(newService(t), flagruntime.BuilderOptions{})

	_, err := b.Build(context.Background(), nil)
	require.ErrorIs(t, err, flagruntime.ErrInvalidContent)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = b.Build(ctx, content(1, objs{}))
	require.ErrorIs(t, err, context.Canceled)
}

func TestBuilderDoesNotChangeBeforeCommit(t *testing.T) {
	t.Parallel()

	var parsed int

	b := flagruntime.NewBuilder(
		newService(t),
		flagruntime.BuilderOptions{OnParse: func(flagruntime.ParseEvent) { parsed++ }},
	)
	objects := baseObjs(t)

	_, err := b.Build(context.Background(), content(1, objects))
	require.NoError(t, err)

	assert.Equal(t, 0, b.CachedParses(), "an uncommitted build leaves the caches alone")

	built, err := b.Build(context.Background(), content(1, objects))
	require.NoError(t, err)

	assert.Equal(t, 8, parsed, "an uncommitted build is redone from scratch: 4 objects parsed twice")
	assert.Equal(t, 4, built.Stats.Rebuilt)
}

// The names aab and abk, and aac and abj, have name hashes that XOR to the same value: a build that told name sets
// apart by such a signature would take two replaced names for no change and keep the removed objects.
func TestBuilderSeesReplacedObjectNamesWhateverTheirHashes(t *testing.T) {
	t.Parallel()

	builder := flagruntime.NewBuilder(newService(t), flagruntime.BuilderOptions{})
	doc := setsObj(t, fset("sa", src("aab"), keyA), fset("sb", src("abk"), keyB))

	first := buildAndCommit(t, builder, content(1, objs{
		"aab": flagsObj(1, "x"), "abk": flagsObj(2, "y"), setsName: doc,
	}))

	require.Equal(t, flagruntime.StateServing, first.Runtime.FlagSets["sa"].State)

	second := buildAndCommit(t, builder, content(2, objs{
		"aac": flagsObj(3, "x"), "abj": flagsObj(4, "y"), setsName: doc,
	}))

	assert.ElementsMatch(t, []string{"aac", "abj", setsName}, mapKeys(second.Runtime.Status.Objects))
	assert.Equal(
		t,
		flagruntime.StateDegraded,
		second.Runtime.FlagSets["sa"].State,
		"its source is gone: the flags are kept",
	)
	assert.Equal(t, flagruntime.StateDegraded, second.Runtime.FlagSets["sb"].State)

	codes := make([]controlplane.Code, 0, len(second.Runtime.Status.Entries))
	for _, entry := range second.Runtime.Status.Entries {
		codes = append(codes, entry.Code)
	}

	assert.Equal(t, []controlplane.Code{controlplane.CodeUnresolvedSource, controlplane.CodeUnresolvedSource}, codes)
}

func mapKeys[V any](in map[string]V) []string {
	out := make([]string, 0, len(in))
	for key := range in {
		out = append(out, key)
	}

	return out
}

// The same bytes that fail to decode under several formats, over many generations, hold one parse result at a time
// and none once the object is gone.
func TestFailedParsesAreReleasedWhateverTheFormat(t *testing.T) {
	t.Parallel()

	builder := flagruntime.NewBuilder(newService(t), flagruntime.BuilderOptions{})
	bad := []byte("x: [unterminated")
	checksum := controlplane.ChecksumOf(bad).Value
	formats := []string{controlplane.FormatJSON, controlplane.FormatYAML, controlplane.FormatTOML}

	for gen := 1; gen <= 30; gen++ {
		object := flagruntime.Object{
			Kind:     flagruntime.KindFlags,
			Format:   formats[gen%len(formats)],
			Bytes:    bad,
			Checksum: checksum,
		}
		built := buildAndCommit(t, builder, content(uint64(gen), objs{"f1": object}))

		assert.Equal(t, flagruntime.ObjectInvalid, built.Runtime.Status.Objects["f1"].State)
		assert.Equal(t, 1, builder.CachedParses(), "generation %d", gen)
	}

	buildAndCommit(t, builder, content(31, objs{}))

	assert.Equal(t, 0, builder.CachedParses())
}

func TestFlagSetListingAKeyTwiceHasOneRoute(t *testing.T) {
	t.Parallel()

	const canary = "CANARY-REPEATED-KEY"

	builder := flagruntime.NewBuilder(newService(t), flagruntime.BuilderOptions{})
	built := buildAndCommit(t, builder, content(1, objs{
		"f1":     flagsObj(1, "x"),
		setsName: setsObj(t, fset("a", src("f1"), canary, "other", canary)),
	}))

	assert.Len(t, built.Runtime.ByKey, 2)
	assert.Equal(t, 2, built.Runtime.FlagSets["a"].APIKeys)
	assert.Equal(t, 2, built.Runtime.Status.FlagSets["a"].APIKeys)
	assert.Equal(t, flagruntime.StateDegraded, built.Runtime.FlagSets["a"].State)

	require.Len(t, built.Runtime.Status.Entries, 1)

	entry := built.Runtime.Status.Entries[0]

	assert.Equal(t, controlplane.CodeDuplicateKey, entry.Code)
	assert.Equal(t, 0, entry.Index)
	assert.Equal(t, "a", entry.Name)
	assert.NotContains(t, entry.Reason, canary)
}

// A flag set that is the running holder of many keys keeps all of them against a new flag set that is first in
// the documents and declares the same keys.
func TestContestedKeysStayWithTheRunningOwner(t *testing.T) {
	t.Parallel()

	const keys = 3000

	declared := make([]string, keys)
	for idx := range declared {
		declared[idx] = fmt.Sprintf("key-%05d", idx)
	}

	builder := flagruntime.NewBuilder(newService(t), flagruntime.BuilderOptions{})
	files := objs{"f1": flagsObj(1, "x")}

	buildAndCommit(t, builder, content(1, files.with(objs{docs2: setsObj(t, fset("holder", src("f1"), declared...))})))

	built := buildAndCommit(t, builder, content(2, files.with(objs{
		docs1: setsObj(t, fset("newcomer", src("f1"), declared...)),
		docs2: setsObj(t, fset("holder", src("f1"), declared...)),
	})))

	assert.Len(t, built.Runtime.ByKey, keys)
	assert.Equal(t, keys, built.Runtime.FlagSets["holder"].APIKeys)
	assert.Equal(t, 0, built.Runtime.FlagSets["newcomer"].APIKeys)

	owner, _ := routedSet(built.Runtime, declared[keys-1])

	assert.Equal(t, "holder", owner.Name)
}

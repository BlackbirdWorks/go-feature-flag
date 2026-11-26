package flagruntime_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/controlplane"
)

func TestFrozenFlagSetDoesNotFollowItsSourcesUntilTheEntryIsValidAgain(t *testing.T) {
	t.Parallel()

	builder := flagruntime.NewBuilder(newService(t), flagruntime.BuilderOptions{})
	good := objs{"f1": flagsObj(1, "x"), setsName: setsObj(t, fset("a", src("f1"), keyA))}
	invalid := setsObj(t, fset("a", nil, keyA))

	first := buildAndCommit(t, builder, content(1, good))
	frozen := buildAndCommit(t, builder, content(2, good.with(objs{setsName: invalid})))

	assert.Equal(t, flagruntime.StateDegraded, frozen.Runtime.FlagSets["a"].State)
	assert.Same(t, first.Runtime.FlagSets["a"].Flags["x"], frozen.Runtime.FlagSets["a"].Flags["x"])

	// the source changes while the entry is invalid: the flag set is not rebuilt and does not serve new flags
	later := buildAndCommit(t, builder, content(3, good.with(objs{setsName: invalid, "f1": flagsObj(9, "x")})))

	assert.Empty(t, later.Rebuilt)
	assert.Same(t, frozen.Runtime.FlagSets["a"], later.Runtime.FlagSets["a"])
	assert.EqualValues(t, 1, value(later.Runtime.FlagSets["a"], "x"))

	// the entry is valid again: the flag set follows its sources
	valid := buildAndCommit(t, builder, content(4, good.with(objs{"f1": flagsObj(9, "x")})))

	assert.Equal(t, flagruntime.StateServing, valid.Runtime.FlagSets["a"].State)
	assert.EqualValues(t, 9, value(valid.Runtime.FlagSets["a"], "x"))
	assert.Equal(t, []string{"a"}, rebuiltNames(valid))
}

func TestPlaceholderRoutesItsKeysAndBecomesRealWhenTheEntryIsValid(t *testing.T) {
	t.Parallel()

	builder := flagruntime.NewBuilder(newService(t), flagruntime.BuilderOptions{})
	files := objs{"f1": flagsObj(1, "x")}

	holder := buildAndCommit(t, builder, content(1, files.with(objs{setsName: setsObj(t, fset("a", nil, keyA))})))

	set, ok := holder.Runtime.FlagSetByKey(keyA)
	require.True(t, ok, "a valid key of an invalid entry reaches the flag set")
	assert.Equal(t, flagruntime.StatePending, set.State)
	assert.Empty(t, set.Flags)
	assert.Equal(t, 1, set.APIKeys)

	// still invalid in the next generation: the placeholder is reused
	again := buildAndCommit(t, builder, content(2, files.with(objs{setsName: setsObj(t, fset("a", nil, keyA))})))

	assert.Same(t, set, again.Runtime.FlagSets["a"])
	assert.Empty(t, again.Rebuilt)

	fixed := buildAndCommit(t, builder, content(3, files.with(objs{setsName: setsObj(t, fset("a", src("f1"), keyA))})))

	assert.Equal(t, flagruntime.StateServing, fixed.Runtime.FlagSets["a"].State)
	assert.Equal(t, []string{"x"}, flagNames(fixed.Runtime.FlagSets["a"]))
}

func TestDocumentsParseOncePerChecksumAndInvalidDocumentsAreRemembered(t *testing.T) {
	t.Parallel()

	var parsed int

	builder := flagruntime.NewBuilder(newService(t), flagruntime.BuilderOptions{
		OnParse: func(event flagruntime.ParseEvent) {
			if event.Kind == flagruntime.KindFlagSets {
				parsed++
			}
		},
	})
	doc := setsObj(t, fset("a", src("f1"), keyA))
	files := objs{"f1": flagsObj(1, "x")}

	// the same bytes under two names: parsed once, the second name is served from the cache
	buildAndCommit(t, builder, content(1, files.with(objs{docs1: doc, docs2: doc})))
	assert.Equal(t, 1, parsed)

	bad := obj(flagruntime.KindFlagSets, []byte("flagSets: [unterminated"))

	// an undecodable document keeps the previous flag sets; the same bad bytes again are not parsed again
	buildAndCommit(t, builder, content(2, files.with(objs{docs1: bad, docs2: doc})))
	again := buildAndCommit(t, builder, content(3, files.with(objs{docs1: bad, docs2: doc})))

	assert.Equal(t, 2, parsed)
	assert.Equal(t, flagruntime.ObjectInvalid, again.Runtime.Status.Objects[docs1].State)

	// back to the good bytes: the table of the held document is still right, no flag set is rebuilt
	back := buildAndCommit(t, builder, content(4, files.with(objs{docs1: doc, docs2: doc})))

	assert.Equal(t, flagruntime.ObjectOK, back.Runtime.Status.Objects[docs1].State)
	assert.Empty(t, back.Rebuilt)
	assert.Equal(t, 2, parsed, "the good bytes were still cached")
}

func TestRepeatedFailuresAreRemembered(t *testing.T) {
	t.Parallel()

	builder := flagruntime.NewBuilder(newService(t), flagruntime.BuilderOptions{})
	files := objs{"f1": flagsObj(1, "x"), setsName: setsObj(t, fset("a", src("f1"), keyA))}

	buildAndCommit(t, builder, content(1, files))

	badFlags := obj(flagruntime.KindFlags, []byte("x: [unterminated"))
	staleDoc := flagruntime.Object{Kind: flagruntime.KindFlagSets, State: flagruntime.ObjectStale, Reason: "etag"}

	first := buildAndCommit(t, builder, content(2, files.with(objs{"f1": badFlags, setsName: staleDoc})))
	second := buildAndCommit(t, builder, content(3, files.with(objs{"f1": badFlags, setsName: staleDoc})))

	assert.Equal(t, flagruntime.ObjectInvalid, second.Runtime.Status.Objects["f1"].State)
	assert.Equal(t, flagruntime.ObjectStale, second.Runtime.Status.Objects[setsName].State)
	assert.Same(t, first.Runtime.Status.Objects["f1"], second.Runtime.Status.Objects["f1"], "nothing is rebuilt")
	assert.Same(t, first.Runtime.Status.Objects[setsName], second.Runtime.Status.Objects[setsName])
	assert.Empty(t, second.Rebuilt)
}

func TestUndecodableDocumentOnColdStartDefinesNothing(t *testing.T) {
	t.Parallel()

	builder := flagruntime.NewBuilder(newService(t), flagruntime.BuilderOptions{})
	built := buildAndCommit(t, builder, content(1, objs{
		"f1":     flagsObj(1, "x"),
		setsName: obj(flagruntime.KindFlagSets, []byte("flagSets: [unterminated")),
	}))

	assert.Empty(t, built.Runtime.FlagSets)
	assert.Equal(t, flagruntime.ObjectInvalid, built.Runtime.Status.Objects[setsName].State)
}

func TestInvalidEntryWithAnAlreadyHeldNameIsReportedOnly(t *testing.T) {
	t.Parallel()

	builder := flagruntime.NewBuilder(newService(t), flagruntime.BuilderOptions{})
	built := buildAndCommit(t, builder, content(1, objs{
		"f1":  flagsObj(1, "x"),
		docs1: setsObj(t, fset("a", src("f1"), keyA)),
		docs2: setsObj(t, fset("a", nil, keyB)),
	}))

	assert.Len(t, built.Runtime.FlagSets, 1)
	assert.Equal(t, flagruntime.StateServing, built.Runtime.FlagSets["a"].State)
	assert.Equal(t, []string{"x"}, flagNames(built.Runtime.FlagSets["a"]))

	_, routed := built.Runtime.FlagSetByKey(keyB)
	assert.False(t, routed, "the key of the entry that lost the name does not route")
}

func TestFrozenAndPlaceholderSetsAreReusedByAFullPass(t *testing.T) {
	t.Parallel()

	builder := flagruntime.NewBuilder(newService(t), flagruntime.BuilderOptions{})
	good := objs{"f1": flagsObj(1, "x"), setsName: setsObj(t, fset("a", src("f1"), keyA))}
	invalid := setsObj(t, fset("a", nil, keyA), fset("b", nil, keyB))

	buildAndCommit(t, builder, content(1, good))

	first := buildAndCommit(t, builder, content(2, good.with(objs{setsName: invalid})))

	// an object is added: the entries are walked again, and both flag sets are carried over
	second := buildAndCommit(t, builder, content(3, good.with(objs{setsName: invalid, extraName: flagsObj(2, "z")})))

	assert.Empty(t, second.Rebuilt)
	assert.Same(t, first.Runtime.FlagSets["a"], second.Runtime.FlagSets["a"])
	assert.Same(t, first.Runtime.FlagSets["b"], second.Runtime.FlagSets["b"])
	assert.Equal(t, flagruntime.StateDegraded, second.Runtime.FlagSets["a"].State)
	assert.Equal(t, flagruntime.StatePending, second.Runtime.FlagSets["b"].State)
}

func TestEngineHashesObjectsThatHaveNoChecksumWithoutChangingTheContent(t *testing.T) {
	t.Parallel()

	harness := newRig(t, flagruntime.Hooks{})
	harness.start()

	files := objs{
		"f1":     {Kind: flagruntime.KindFlags, Format: controlplane.FormatYAML, Bytes: flagsYAML(1, "x")},
		setsName: {Kind: flagruntime.KindFlagSets, Bytes: setsObj(t, fset("a", src("f1"), keyA)).Bytes},
	}

	res := harness.feed(content(1, files))

	require.Equal(t, flagruntime.OutcomeApplied, res.Outcome)
	assert.Empty(t, files["f1"].Checksum, "the content of the caller is not changed")
	assert.NotEmpty(t, harness.holder.Load().Status.Objects["f1"].Checksum)

	again := harness.feed(content(1, files))

	assert.Equal(t, flagruntime.CodeDuplicate, again.Code, "the same bytes are the same content")
}

package flagruntime

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
)

const (
	setsKey  = "sets"
	keyStay  = "key-stay"
	stayName = "stay"
)

const internalFlags = "x:\n  variations:\n    v: 1\n  defaultRule:\n    variation: v\n"

func internalDoc(t *testing.T, sets ...controlplane.FlagSet) Object {
	t.Helper()

	data, err := controlplane.MarshalFlagSet(&controlplane.FlagSetDocument{FlagSets: sets})
	require.NoError(t, err)

	return Object{Kind: KindFlagSets, Format: controlplane.FormatYAML, Bytes: data}
}

// A long run of flag sets created and withdrawn leaves a bounded state: no map of the builder and no part of the
// status grows with the number of flag sets that ever existed.
func TestBuilderStateStaysBoundedUnderCreateAndWithdrawCycles(t *testing.T) {
	t.Parallel()

	const (
		cycles  = 10000
		wantCap = 8
	)

	svc := flagdef.NewService()
	t.Cleanup(svc.Close)

	builder := NewBuilder(svc, BuilderOptions{WithdrawnCap: wantCap})
	flags := Object{Kind: KindFlags, Format: controlplane.FormatYAML, Bytes: []byte(internalFlags)}

	generation := uint64(0)

	build := func(sets ...controlplane.FlagSet) *Built {
		generation++

		built, err := builder.Build(context.Background(), &Content{
			Generation: generation,
			Objects:    map[string]Object{"f1": flags, setsKey: internalDoc(t, sets...)},
		})
		require.NoError(t, err)

		builder.Commit(built)

		return built
	}

	build(controlplane.FlagSet{Name: stayName, Sources: []string{"f1"}, APIKeys: []string{keyStay}})

	for cycle := range cycles {
		name := fmt.Sprintf("tmp-%d", cycle)
		stay := controlplane.FlagSet{Name: stayName, Sources: []string{"f1"}, APIKeys: []string{keyStay}}

		build(stay, controlplane.FlagSet{Name: name, Sources: []string{"f1"}, APIKeys: []string{"key-" + name}})
		build(stay)
	}

	state := builder.cur

	assert.Len(t, state.sets, 1)
	assert.Len(t, state.flagSets, 1)
	assert.Len(t, state.byKey, 1)
	assert.Len(t, state.setStatus, 1)
	assert.Len(t, state.rev, 1)
	assert.Len(t, state.objects, 1)
	assert.Len(t, state.docs, 1)
	assert.Len(t, state.objStatus, 2)
	assert.Empty(t, state.entries)
	assert.Len(t, state.withdrawn, wantCap)
	assert.Equal(t, fmt.Sprintf("tmp-%d", cycles-1), state.withdrawn[wantCap-1].Name, "the newest is kept")
	assert.LessOrEqual(t, builder.CachedParses(), 2, "parse results of withdrawn documents are dropped")
}

// A flag that did not change in a changed object is held once: the parse result that the cache keeps for the new
// checksum points to the running flag, not to a second copy.
func TestParseCacheHoldsTheRunningPointersOfUnchangedFlags(t *testing.T) {
	t.Parallel()

	svc := flagdef.NewService()
	t.Cleanup(svc.Close)

	builder := NewBuilder(svc, BuilderOptions{})
	doc := internalDoc(t, controlplane.FlagSet{Name: "a", Sources: []string{"f1"}, APIKeys: []string{"key-a"}})
	flagsOf := func(y int) Object {
		data := []byte(fmt.Sprintf("x:\n  variations:\n    v: 1\n  defaultRule:\n    variation: v\ny:\n"+
			"  variations:\n    v: %d\n  defaultRule:\n    variation: v\n", y))

		return Object{Kind: KindFlags, Format: "yaml", Checksum: controlplane.ChecksumOf(data).Value, Bytes: data}
	}

	build := func(gen uint64, y int) *Built {
		built, err := builder.Build(context.Background(), &Content{
			Generation: gen, Objects: map[string]Object{"f1": flagsOf(y), setsKey: doc},
		})
		require.NoError(t, err)

		builder.Commit(built)

		return built
	}

	build(1, 1)

	second := build(2, 2)
	running := second.Runtime.FlagSets["a"].Flags

	entry := builder.flags.lookup("yaml\x00" + flagsOf(2).Checksum)
	require.NotNil(t, entry)

	assert.Same(t, running["x"], entry.value.valid["x"], "the unchanged flag is held once")
	assert.Same(t, running["y"], entry.value.valid["y"], "the changed flag is the cached one")
}

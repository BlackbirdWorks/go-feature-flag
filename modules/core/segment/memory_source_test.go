package segment_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

var errBoom = errors.New("boom")

func TestMemberSetBuilder(t *testing.T) {
	t.Parallel()

	var builder segment.MemberSetBuilder

	empty := builder.Build()
	assert.Equal(t, 0, empty.Len(), "an unused builder builds an empty set")

	assert.True(t, builder.Add("a"))
	assert.True(t, builder.Add("b"))
	assert.False(t, builder.Add("a"), "a duplicate is not new")
	assert.Equal(t, 2, builder.Len())

	set := builder.Build()
	assert.Equal(t, 2, set.Len())
	assert.True(t, set.Contains("b"))
	assert.Equal(t, 0, builder.Len(), "Build resets the builder")
	assert.ElementsMatch(t, []string{"a", "b"}, collectSeq(set.All()))

	for range set.All() {
		break // an early stop is honoured
	}
}

func collectSeq(seq func(func(string) bool)) []string {
	var out []string

	seq(func(id string) bool {
		out = append(out, id)

		return true
	})

	return out
}

func TestMemorySourceVersions(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	src := segment.NewMemorySource(map[string][]string{segAlpha: {"a"}})

	first, err := src.Version(ctx, segAlpha)
	require.NoError(t, err)

	absent, err := src.Version(ctx, segGhost)
	require.NoError(t, err)
	assert.Equal(t, segment.AbsentVersion, absent)

	src.PinVersion(segAlpha, "pinned")

	pinned, err := src.Version(ctx, segAlpha)
	require.NoError(t, err)
	assert.Equal(t, "pinned", pinned)

	src.PinVersion(segAlpha, "")

	unpinned, err := src.Version(ctx, segAlpha)
	require.NoError(t, err)
	assert.Equal(t, "2", unpinned, "unpinned: a fresh counter version")

	src.Delete(segAlpha)

	deleted, err := src.Version(ctx, segAlpha)
	require.NoError(t, err)
	assert.Equal(t, segment.AbsentVersion, deleted)
	assert.NotEqual(t, first, deleted)
	assert.Equal(t, 5, src.Calls(segment.OpVersion), "every call is counted")
}

func TestMemorySourceFailuresAndHook(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	src := segment.NewMemorySource(map[string][]string{segAlpha: {"a", "b", "c"}})

	src.Fail(segment.OpMembers, errBoom)

	_, err := collect(src, segAlpha)
	require.ErrorIs(t, err, errBoom)

	src.Fail(segment.OpMembers, nil)

	var seen []int

	src.SetMembersHook(func(name string, yielded int) {
		assert.Equal(t, segAlpha, name)

		seen = append(seen, yielded)
	})
	src.FailMembersAfter(2, errBoom)

	got, err := collect(src, segAlpha)
	require.ErrorIs(t, err, errBoom)
	assert.Len(t, got, 2, "two members before the failure")
	assert.Equal(t, []int{0, 1, 2}, seen)

	src.FailMembersAfter(-1, nil)

	all, err := collect(src, segAlpha)
	require.NoError(t, err)
	assert.Len(t, all, 3)

	src.Fail(segment.OpContains, errBoom)
	require.ErrorIs(t, src.Contains(ctx, nil), errBoom)
	assert.Equal(t, 1, src.Calls(segment.OpContains))
}

func collect(src *segment.MemorySource, name string) ([]string, error) {
	var ids []string

	for id, err := range src.Members(context.Background(), name) {
		if err != nil {
			return ids, err
		}

		ids = append(ids, id)
	}

	return ids, nil
}

func TestMemorySourceLookupAndExists(t *testing.T) {
	t.Parallel()

	src := segment.NewMemorySource(map[string][]string{segAlpha: {"a"}})
	lookups := []segment.Lookup{
		{Segment: segAlpha, ID: "a"},
		{Segment: segAlpha, ID: "z"},
		{Segment: segGhost, ID: "a"},
	}

	require.NoError(t, src.Contains(context.Background(), lookups))
	assert.Equal(t, []segment.State{segment.Member, segment.NotMember, segment.Unknown},
		[]segment.State{lookups[0].State, lookups[1].State, lookups[2].State})

	states, err := src.Exists(context.Background(), []string{segAlpha, segGhost})
	require.NoError(t, err)
	assert.Equal(t, []segment.State{segment.Member, segment.NotMember}, states)
}

func TestMemorySourceChanges(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	src := segment.NewMemorySource(nil)

	changes, err := src.Changes(ctx)
	require.NoError(t, err)

	// the subscription exists once Changes returned: these writes are not lost, and the two of alpha coalesce
	src.Set(segAlpha, []string{"a"})
	src.Set(segAlpha, []string{"a", "b"})
	src.PinVersion("beta", "b1")

	var got []segment.Change

	for change, err := range changes {
		require.NoError(t, err)

		got = append(got, change)
		if len(got) == 2 {
			break
		}
	}

	assert.Equal(t, []segment.Change{{Segment: segAlpha, Version: "2"}, {Segment: "beta", Version: "b1"}}, got)

	ended, stop := context.WithCancel(context.Background())
	stream, err := src.Changes(ended)
	require.NoError(t, err)

	stop()

	for _, err := range stream {
		require.ErrorIs(t, err, context.Canceled)
	}

	_, err = src.Changes(ended)
	require.ErrorIs(t, err, context.Canceled, "no subscription on an ended context")
}

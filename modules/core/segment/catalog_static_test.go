package segment_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

const memberAlice = "alice"

func TestCatalogMember(t *testing.T) {
	t.Parallel()

	catalog, err := segment.NewCatalogWithSets(
		map[string]*segment.MemberSet{segBeta: segment.NewMemberSet([]string{memberAlice})},
		segment.Definition{Name: segBeta, Type: segment.TypeFile},
		segment.Definition{Name: "audience", Type: segment.TypeRedis},
	)
	require.NoError(t, err)

	tests := []struct {
		catalog    *segment.Catalog
		name       string
		segment    string
		id         string
		wantState  segment.State
		wantStatic bool
	}{
		{
			name:       "member",
			catalog:    catalog,
			segment:    segBeta,
			id:         memberAlice,
			wantState:  segment.Member,
			wantStatic: true,
		},
		{
			name:       "not a member",
			catalog:    catalog,
			segment:    segBeta,
			id:         "bob",
			wantState:  segment.NotMember,
			wantStatic: true,
		},
		{name: "a dynamic segment is not static", catalog: catalog, segment: "audience", id: memberAlice},
		{name: "a segment that is not defined", catalog: catalog, segment: "ghost", id: memberAlice},
		{name: "no catalog", segment: segBeta, id: memberAlice},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			state, static := tt.catalog.Member(tt.segment, tt.id)

			assert.Equal(t, tt.wantState, state)
			assert.Equal(t, tt.wantStatic, static)
		})
	}
}

func TestCatalogWithSetsRejectsMembersOfAnUndefinedSegment(t *testing.T) {
	t.Parallel()

	_, err := segment.NewCatalogWithSets(
		map[string]*segment.MemberSet{"ghost": segment.NewMemberSet(nil)},
		segment.Definition{Name: segBeta, Type: segment.TypeFile},
	)

	require.ErrorIs(t, err, segment.ErrInvalidDefinition)

	_, err = segment.NewCatalogWithSets(nil, segment.Definition{Name: "bad name", Type: segment.TypeFile})
	require.ErrorIs(t, err, segment.ErrInvalidDefinition)
}

// The lookup of a static segment allocates nothing.
//
//nolint:paralleltest // AllocsPerRun counts allocations from concurrent tests across the process
func TestCatalogMemberAllocatesNothing(t *testing.T) {
	catalog, err := segment.NewCatalogWithSets(
		map[string]*segment.MemberSet{segBeta: segment.NewMemberSet([]string{memberAlice})},
		segment.Definition{Name: segBeta, Type: segment.TypeFile},
	)
	require.NoError(t, err)

	assert.Zero(t, testing.AllocsPerRun(100, func() { _, _ = catalog.Member(segBeta, memberAlice) }))
}

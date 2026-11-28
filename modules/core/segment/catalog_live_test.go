package segment_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

const (
	segLive      = "live"
	segLookup    = "lookup"
	segFixed     = "fixed"
	segUndefined = "undefined"
	memberRob    = "rob"
)

type fixedLive struct {
	state  segment.State
	status segment.Status
}

func (f fixedLive) Answer(string) (segment.State, segment.Status) { return f.state, f.status }

func TestCatalogAnswer(t *testing.T) {
	t.Parallel()

	catalog, err := segment.NewCatalogWithLive(
		map[string]*segment.MemberSet{segFixed: segment.NewMemberSet([]string{memberAlice})},
		map[string]segment.Live{segLive: fixedLive{segment.Member, segment.StatusStale}},
		segment.Definition{Name: segFixed, Type: segment.TypeFile},
		segment.Definition{Name: segLive, Type: segment.TypeRedis},
		segment.Definition{Name: segLookup, Type: segment.TypeRedis},
	)
	require.NoError(t, err)

	tests := []struct {
		name       string
		segment    string
		id         string
		wantState  segment.State
		wantStatus segment.Status
		wantOK     bool
	}{
		{
			name:       "static member",
			segment:    segFixed,
			id:         memberAlice,
			wantState:  segment.Member,
			wantStatus: segment.StatusHealthy,
			wantOK:     true,
		},
		{
			name:       "static other",
			segment:    segFixed,
			id:         memberRob,
			wantState:  segment.NotMember,
			wantStatus: segment.StatusHealthy,
			wantOK:     true,
		},
		{
			name:       segLive,
			segment:    segLive,
			id:         memberRob,
			wantState:  segment.Member,
			wantStatus: segment.StatusStale,
			wantOK:     true,
		},
		{
			name:       "provider",
			segment:    segLookup,
			id:         memberAlice,
			wantState:  segment.Unknown,
			wantStatus: segment.StatusNone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			state, status, ok := catalog.Answer(tt.segment, tt.id)

			assert.Equal(t, tt.wantState, state)
			assert.Equal(t, tt.wantStatus, status)
			assert.Equal(t, tt.wantOK, ok)
		})
	}

	var none *segment.Catalog

	_, _, ok := none.Answer(segFixed, memberAlice)
	assert.False(t, ok)
}

func TestNewCatalogWithLive(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantErr error
		live    map[string]segment.Live
		sets    map[string]*segment.MemberSet
		name    string
	}{
		{name: "defined", live: map[string]segment.Live{"a": fixedLive{}}},
		{
			name:    "undefined",
			live:    map[string]segment.Live{segUndefined: fixedLive{}},
			wantErr: segment.ErrInvalidDefinition,
		},
		{
			name: "also static", live: map[string]segment.Live{"a": fixedLive{}},
			sets: map[string]*segment.MemberSet{"a": segment.NewMemberSet(nil)}, wantErr: segment.ErrInvalidDefinition,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := segment.NewCatalogWithLive(
				tt.sets,
				tt.live,
				segment.Definition{Name: "a", Type: segment.TypeRedis},
			)

			if tt.wantErr == nil {
				require.NoError(t, err)

				return
			}

			require.ErrorIs(t, err, tt.wantErr)
		})
	}
}

func TestNewCatalogWithLiveChecksTheDefinitions(t *testing.T) {
	t.Parallel()

	catalog, err := segment.NewCatalogWithLive(nil, nil, segment.Definition{Name: "Bad Name", Type: segment.TypeRedis})

	require.ErrorIs(t, err, segment.ErrInvalidDefinition)
	assert.Nil(t, catalog)
}

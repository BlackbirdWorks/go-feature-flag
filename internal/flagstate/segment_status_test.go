package flagstate_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/flagstate"
	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

const itsOwn = "its-own"

func TestMetadataWithStatus(t *testing.T) {
	t.Parallel()

	own := map[string]any{flagstate.MetadataSegmentStatus: itsOwn, "a": 1}

	tests := []struct {
		state        flagstate.FlagState
		wantMetadata map[string]any
		name         string
		wantSame     bool
	}{
		{name: "none", state: flagstate.FlagState{Metadata: own}, wantMetadata: own, wantSame: true},
		{
			name: "healthy", state: flagstate.FlagState{Metadata: own, SegmentStatus: segment.StatusHealthy},
			wantMetadata: own, wantSame: true,
		},
		{
			name:         "store error: a copy with the status, over a key of the flag's own",
			state:        flagstate.FlagState{Metadata: own, SegmentStatus: segment.StatusStoreError},
			wantMetadata: map[string]any{flagstate.MetadataSegmentStatus: "STORE_ERROR", "a": 1},
		},
		{
			name:         "stale, no metadata",
			state:        flagstate.FlagState{SegmentStatus: segment.StatusStale},
			wantMetadata: map[string]any{flagstate.MetadataSegmentStatus: "STALE"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.state.MetadataWithStatus()

			assert.Equal(t, tt.wantMetadata, got)

			assert.Equal(
				t, tt.wantSame, reflect.ValueOf(got).Pointer() == reflect.ValueOf(own).Pointer(),
				"the map itself is sent when there is no status to add",
			)
		})
	}

	assert.Equal(t, itsOwn, own[flagstate.MetadataSegmentStatus], "the metadata of the flag is never changed")
}

// not parallel: AllocsPerRun cannot run in a parallel test.
//
//nolint:paralleltest // AllocsPerRun counts allocations from concurrent tests across the process
func TestMetadataWithStatusAllocatesNothingForAHealthyStatus(t *testing.T) {
	own := map[string]any{"a": 1}
	state := flagstate.FlagState{Metadata: own, SegmentStatus: segment.StatusHealthy}

	assert.InDelta(t, 0, testing.AllocsPerRun(100, func() { _ = state.MetadataWithStatus() }), 0)
}

func TestAllFlagsSendsTheStatusWithoutChangingTheState(t *testing.T) {
	t.Parallel()

	own := map[string]any{"a": 1}
	all := flagstate.NewAllFlags()
	all.AddFlag("f", &flagstate.FlagState{Metadata: own, SegmentStatus: segment.StatusNotConfigured})

	data, err := json.Marshal(all)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"semaphore_segmentStatus":"NOT_CONFIGURED"`)
	assert.Equal(t, map[string]any{"a": 1}, own, "the metadata of the flag is not changed")
	assert.Equal(t, map[string]any{"a": 1}, all.GetFlags()["f"].Metadata, "nor the state that is kept")
}

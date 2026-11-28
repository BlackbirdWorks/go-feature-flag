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

const (
	itsOwn    = "its-own"
	statusKey = flagstate.MetadataSegmentStatus
)

func TestMetadataWithStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input     map[string]any
		wantInput map[string]any // the input map after the call: the status is written into it, not into a copy
		wantSent  map[string]any
		name      string
		status    segment.Status
	}{
		{
			name: "none", input: map[string]any{"a": 1}, status: segment.StatusNone,
			wantInput: map[string]any{"a": 1}, wantSent: map[string]any{"a": 1},
		},
		{
			name: "healthy", input: map[string]any{"a": 1}, status: segment.StatusHealthy,
			wantInput: map[string]any{"a": 1}, wantSent: map[string]any{"a": 1},
		},
		{
			name: "store error, over a key of the same name", input: map[string]any{statusKey: itsOwn, "a": 1},
			status:    segment.StatusStoreError,
			wantInput: map[string]any{statusKey: "STORE_ERROR", "a": 1},
			wantSent:  map[string]any{statusKey: "STORE_ERROR", "a": 1},
		},
		{
			name: "stale, no metadata", status: segment.StatusStale,
			wantSent: map[string]any{statusKey: "STALE"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			state := flagstate.FlagState{Metadata: tt.input, SegmentStatus: tt.status}

			got := state.MetadataWithStatus()

			assert.Equal(t, tt.wantSent, got)
			assert.Equal(t, tt.wantInput, tt.input, "the map of the evaluation after the call")

			if tt.input != nil {
				assert.Equal(
					t, reflect.ValueOf(tt.input).Pointer(), reflect.ValueOf(got).Pointer(),
					"no second map: the status goes into the map of the evaluation",
				)
			}
		})
	}
}

// not parallel: AllocsPerRun cannot run in a parallel test.
//
//nolint:paralleltest // AllocsPerRun counts allocations from concurrent tests across the process
func TestMetadataWithStatusAllocatesNothingForAHealthyStatus(t *testing.T) {
	own := map[string]any{"a": 1}
	state := flagstate.FlagState{Metadata: own, SegmentStatus: segment.StatusHealthy}

	assert.InDelta(t, 0, testing.AllocsPerRun(100, func() { _ = state.MetadataWithStatus() }), 0)

	// an unhealthy status goes into the map of the evaluation: no copy of it (room for the key is there already)
	sized := make(map[string]any, 8)
	sized["a"] = 1
	unhealthy := flagstate.FlagState{Metadata: sized, SegmentStatus: segment.StatusStoreError}

	assert.InDelta(t, 0, testing.AllocsPerRun(100, func() { _ = unhealthy.MetadataWithStatus() }), 0)
}

func TestAllFlagsSendsTheStatusInTheMapOfTheEvaluation(t *testing.T) {
	t.Parallel()

	own := map[string]any{"a": 1}
	all := flagstate.NewAllFlags()
	all.AddFlag("f", &flagstate.FlagState{Metadata: own, SegmentStatus: segment.StatusNotConfigured})

	data, err := json.Marshal(all)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"semaphore_segmentStatus":"NOT_CONFIGURED"`)
	assert.Equal(
		t,
		map[string]any{"a": 1, statusKey: "NOT_CONFIGURED"},
		own,
		"the map of the evaluation has the status",
	)
}

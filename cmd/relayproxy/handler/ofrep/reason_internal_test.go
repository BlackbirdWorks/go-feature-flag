package ofrep

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
)

func Test_ofrepReasonMapping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		reason     flagdef.ResolutionReason
		wantSingle string
		wantBulk   string
	}{
		{
			name:       "STATIC stays STATIC",
			reason:     flagdef.ReasonStatic,
			wantSingle: flagdef.ReasonStatic,
			wantBulk:   flagdef.ReasonStatic,
		},
		{
			name:       "DEFAULT maps to STATIC",
			reason:     flagdef.ReasonDefault,
			wantSingle: flagdef.ReasonStatic,
			wantBulk:   flagdef.ReasonStatic,
		},
		{
			name:       "TARGETING_MATCH stays TARGETING_MATCH",
			reason:     flagdef.ReasonTargetingMatch,
			wantSingle: flagdef.ReasonTargetingMatch,
			wantBulk:   flagdef.ReasonTargetingMatch,
		},
		{
			name:       "SPLIT stays SPLIT",
			reason:     flagdef.ReasonSplit,
			wantSingle: flagdef.ReasonSplit,
			wantBulk:   flagdef.ReasonSplit,
		},
		{
			name:       "TARGETING_MATCH_SPLIT maps to SPLIT",
			reason:     flagdef.ReasonTargetingMatchSplit,
			wantSingle: flagdef.ReasonSplit,
			wantBulk:   flagdef.ReasonSplit,
		},
		{
			name:       "DISABLED stays DISABLED",
			reason:     flagdef.ReasonDisabled,
			wantSingle: flagdef.ReasonDisabled,
			wantBulk:   flagdef.ReasonDisabled,
		},
		{
			name:       "UNKNOWN stays UNKNOWN",
			reason:     flagdef.ReasonUnknown,
			wantSingle: flagdef.ReasonUnknown,
			wantBulk:   flagdef.ReasonUnknown,
		},
		{
			name:       "OFFLINE maps to UNKNOWN",
			reason:     flagdef.ReasonOffline,
			wantSingle: flagdef.ReasonUnknown,
			wantBulk:   flagdef.ReasonUnknown,
		},
		{
			name:       "ERROR maps to UNKNOWN on single and stays ERROR on bulk",
			reason:     flagdef.ReasonError,
			wantSingle: flagdef.ReasonUnknown,
			wantBulk:   flagdef.ReasonError,
		},
		{
			name:       "empty reason maps to UNKNOWN",
			reason:     "",
			wantSingle: flagdef.ReasonUnknown,
			wantBulk:   flagdef.ReasonUnknown,
		},
		{
			name:       "unrecognized reason maps to UNKNOWN",
			reason:     "UNEXPECTED_REASON",
			wantSingle: flagdef.ReasonUnknown,
			wantBulk:   flagdef.ReasonUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.wantSingle, ofrepReason(tt.reason))
			assert.Equal(t, tt.wantBulk, ofrepBulkReason(tt.reason))
		})
	}
}

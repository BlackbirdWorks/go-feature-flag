package flagstate_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/internal/flagstate"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

func TestFromFlagEvaluation(t *testing.T) {
	t.Parallel()

	evaluatedAt := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)

	tests := []struct {
		expected      flagstate.FlagState
		flagCtx       flagdef.Context
		evaluationCtx ffcontext.Context
		currentFlag   flagdef.Flag
		name          string
		key           string
	}{
		{
			name:          "Flag is disabled",
			key:           fixtures.UserKeyTest,
			evaluationCtx: ffcontext.NewEvaluationContext("user-key"),
			flagCtx:       flagdef.Context{Service: flagdef.NewService()},
			currentFlag: &flagdef.InternalFlag{
				Disable: new(true),
			},
			expected: flagstate.FlagState{
				Timestamp:    evaluatedAt.Unix(),
				TrackEvents:  true,
				Failed:       false,
				ErrorCode:    "",
				ErrorDetails: "",
				Reason:       flagdef.ReasonDisabled,
				Metadata:     nil,
			},
		},
		{
			name:          "Flag evaluation error",
			key:           fixtures.UserKeyTest,
			evaluationCtx: ffcontext.NewEvaluationContext(""),
			flagCtx:       flagdef.Context{Service: flagdef.NewService()},
			currentFlag: &flagdef.InternalFlag{
				Disable: new(false),
				Variations: &map[string]*any{
					fixtures.VarOne: new(any(1)),
					fixtures.VarTwo: new(any(2)),
				},
				DefaultRule: &flagdef.Rule{
					Percentages: &map[string]float64{
						fixtures.VarOne: 50,
						fixtures.VarTwo: 50,
					},
				},
			},
			expected: flagstate.FlagState{
				Timestamp:    evaluatedAt.Unix(),
				TrackEvents:  true,
				Failed:       true,
				ErrorCode:    flagdef.ErrorCodeTargetingKeyMissing,
				ErrorDetails: "Error: Empty targeting key",
				Reason:       flagdef.ReasonError,
				Metadata:     nil,
			},
		},
		{
			name:          "Flag evaluation valid type",
			key:           fixtures.UserKeyTest,
			evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyMy),
			flagCtx:       flagdef.Context{Service: flagdef.NewService()},
			currentFlag: &flagdef.InternalFlag{
				Disable: new(false),
				Variations: &map[string]*any{
					fixtures.VarOne: new(any(1)),
					fixtures.VarTwo: new(any(2)),
				},
				DefaultRule: &flagdef.Rule{
					VariationResult: new(fixtures.VarOne),
				},
			},
			expected: flagstate.FlagState{
				Value:         1,
				VariationType: fixtures.VarOne,
				Timestamp:     evaluatedAt.Unix(),
				TrackEvents:   true,
				Failed:        false,
				Reason:        flagdef.ReasonStatic,
				Metadata:      nil,
			},
		},
		{
			name:          "Flag evaluation invalid type",
			key:           fixtures.UserKeyTest,
			evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyMy),
			flagCtx:       flagdef.Context{Service: flagdef.NewService()},
			currentFlag: &flagdef.InternalFlag{
				Disable: new(false),
				Variations: &map[string]*any{
					fixtures.VarOne: new(any(
						map[bool]*any{true: new(any(1))},
					)),
					fixtures.VarTwo: new(any(
						map[bool]*any{true: new(any(2))},
					)),
				},
				DefaultRule: &flagdef.Rule{
					VariationResult: new(fixtures.VarOne),
				},
			},
			expected: flagstate.FlagState{
				VariationType: "SdkDefault",
				Timestamp:     evaluatedAt.Unix(),
				TrackEvents:   true,
				Failed:        true,
				Reason:        flagdef.ReasonError,
				ErrorCode:     flagdef.ErrorCodeTypeMismatch,
				Metadata:      nil,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := flagstate.FromFlagEvaluationAt(
				evaluatedAt,
				tt.key,
				tt.evaluationCtx,
				tt.flagCtx,
				tt.currentFlag,
			)
			assert.Equal(t, tt.expected, got)
		})
	}
}

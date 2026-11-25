package flagstate_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/internal/flagstate"
	"github.com/blackbirdworks/semaphore/modules/core/flag"
)

const (
	myKey   = "my-key"
	testKey = "test-key"
	var12   = "var1"
	var22   = "var2"
)

func TestFromFlagEvaluation(t *testing.T) {
	t.Parallel()

	evaluatedAt := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)

	tests := []struct {
		expected      flagstate.FlagState
		flagCtx       flag.Context
		evaluationCtx ffcontext.Context
		currentFlag   flag.Flag
		name          string
		key           string
	}{
		{
			name:          "Flag is disabled",
			key:           testKey,
			evaluationCtx: ffcontext.NewEvaluationContext("user-key"),
			flagCtx:       flag.Context{Service: flag.NewService()},
			currentFlag: &flag.InternalFlag{
				Disable: new(true),
			},
			expected: flagstate.FlagState{
				Timestamp:    evaluatedAt.Unix(),
				TrackEvents:  true,
				Failed:       false,
				ErrorCode:    "",
				ErrorDetails: "",
				Reason:       flag.ReasonDisabled,
				Metadata:     nil,
			},
		},
		{
			name:          "Flag evaluation error",
			key:           testKey,
			evaluationCtx: ffcontext.NewEvaluationContext(""),
			flagCtx:       flag.Context{Service: flag.NewService()},
			currentFlag: &flag.InternalFlag{
				Disable: new(false),
				Variations: &map[string]*any{
					var12: new(any(1)),
					var22: new(any(2)),
				},
				DefaultRule: &flag.Rule{
					Percentages: &map[string]float64{
						var12: 50,
						var22: 50,
					},
				},
			},
			expected: flagstate.FlagState{
				Timestamp:    evaluatedAt.Unix(),
				TrackEvents:  true,
				Failed:       true,
				ErrorCode:    flag.ErrorCodeTargetingKeyMissing,
				ErrorDetails: "Error: Empty targeting key",
				Reason:       flag.ReasonError,
				Metadata:     nil,
			},
		},
		{
			name:          "Flag evaluation valid type",
			key:           testKey,
			evaluationCtx: ffcontext.NewEvaluationContext(myKey),
			flagCtx:       flag.Context{Service: flag.NewService()},
			currentFlag: &flag.InternalFlag{
				Disable: new(false),
				Variations: &map[string]*any{
					var12: new(any(1)),
					var22: new(any(2)),
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(var12),
				},
			},
			expected: flagstate.FlagState{
				Value:         1,
				VariationType: var12,
				Timestamp:     evaluatedAt.Unix(),
				TrackEvents:   true,
				Failed:        false,
				Reason:        flag.ReasonStatic,
				Metadata:      nil,
			},
		},
		{
			name:          "Flag evaluation invalid type",
			key:           testKey,
			evaluationCtx: ffcontext.NewEvaluationContext(myKey),
			flagCtx:       flag.Context{Service: flag.NewService()},
			currentFlag: &flag.InternalFlag{
				Disable: new(false),
				Variations: &map[string]*any{
					var12: new(any(
						map[bool]*any{true: new(any(1))},
					)),
					var22: new(any(
						map[bool]*any{true: new(any(2))},
					)),
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(var12),
				},
			},
			expected: flagstate.FlagState{
				VariationType: "SdkDefault",
				Timestamp:     evaluatedAt.Unix(),
				TrackEvents:   true,
				Failed:        true,
				Reason:        flag.ReasonError,
				ErrorCode:     flag.ErrorCodeTypeMismatch,
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

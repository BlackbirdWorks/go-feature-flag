package flagstate_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/internal/flagstate"
	"github.com/blackbirdworks/semaphore/modules/core/flag"
	"github.com/blackbirdworks/semaphore/modules/core/testutils/testconvert"
)

//nolint:paralleltest,tparallel // builds wall-clock relative fixtures (time.Now) before its subtests, which run later and would see a shifted clock
func TestFromFlagEvaluation(t *testing.T) {

	tests := []struct {
		name          string
		key           string
		evaluationCtx ffcontext.Context
		flagCtx       flag.Context
		currentFlag   flag.Flag
		expected      flagstate.FlagState
	}{
		{
			name:          "Flag is disabled",
			key:           "test-key",
			evaluationCtx: ffcontext.NewEvaluationContext("user-key"),
			flagCtx:       flag.Context{},
			currentFlag: &flag.InternalFlag{
				Disable: new(true),
			},
			expected: flagstate.FlagState{
				Timestamp:    time.Now().Unix(),
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
			key:           "test-key",
			evaluationCtx: ffcontext.NewEvaluationContext(""),
			flagCtx:       flag.Context{},
			currentFlag: &flag.InternalFlag{
				Disable: new(false),
				Variations: &map[string]*any{
					"var1": testconvert.Interface(1),
					"var2": testconvert.Interface(2),
				},
				DefaultRule: &flag.Rule{
					Percentages: &map[string]float64{
						"var1": 50,
						"var2": 50,
					},
				},
			},
			expected: flagstate.FlagState{
				Timestamp:    time.Now().Unix(),
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
			key:           "test-key",
			evaluationCtx: ffcontext.NewEvaluationContext("my-key"),
			flagCtx:       flag.Context{},
			currentFlag: &flag.InternalFlag{
				Disable: new(false),
				Variations: &map[string]*any{
					"var1": testconvert.Interface(1),
					"var2": testconvert.Interface(2),
				},
				DefaultRule: &flag.Rule{
					VariationResult: new("var1"),
				},
			},
			expected: flagstate.FlagState{
				Value:         1,
				VariationType: "var1",
				Timestamp:     time.Now().Unix(),
				TrackEvents:   true,
				Failed:        false,
				Reason:        flag.ReasonStatic,
				Metadata:      nil,
			},
		},
		{
			name:          "Flag evaluation invalid type",
			key:           "test-key",
			evaluationCtx: ffcontext.NewEvaluationContext("my-key"),
			flagCtx:       flag.Context{},
			currentFlag: &flag.InternalFlag{
				Disable: new(false),
				Variations: &map[string]*any{
					"var1": testconvert.Interface(
						map[bool]*any{true: testconvert.Interface(1)},
					),
					"var2": testconvert.Interface(
						map[bool]*any{true: testconvert.Interface(2)},
					),
				},
				DefaultRule: &flag.Rule{
					VariationResult: new("var1"),
				},
			},
			expected: flagstate.FlagState{
				VariationType: "SdkDefault",
				Timestamp:     time.Now().Unix(),
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

			got := flagstate.FromFlagEvaluation(
				tt.key,
				tt.evaluationCtx,
				tt.flagCtx,
				tt.currentFlag,
			)
			assert.Equal(t, tt.expected, got)
		})
	}
}

package flag_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/flag"
)

const (
	disabled2  = "disabled"
	enabled2   = "enabled"
	keyEqAdmin = "key eq \"admin\""
	team123    = "team-123"
)

func TestInternalFlag_RequiresBucketing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		flag     flag.InternalFlag
		name     string
		expected bool
	}{
		{
			name: "Should not require bucketing - static variation only",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					enabled2:  new(any(true)),
					disabled2: new(any(false)),
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(disabled2),
				},
			},
			expected: false,
		},
		{
			name: "Should require bucketing - default rule has percentages",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					enabled2:  new(any(true)),
					disabled2: new(any(false)),
				},
				DefaultRule: &flag.Rule{
					Percentages: &map[string]float64{
						enabled2:  20,
						disabled2: 80,
					},
				},
			},
			expected: true,
		},
		{
			name: "Should require bucketing - targeting rule has percentages",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					enabled2:  new(any(true)),
					disabled2: new(any(false)),
				},
				Rules: &[]flag.Rule{
					{
						Query: new(keyEqAdmin),
						Percentages: &map[string]float64{
							enabled2:  50,
							disabled2: 50,
						},
					},
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(disabled2),
				},
			},
			expected: true,
		},
		{
			name: "Should require bucketing - has progressive rollout",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					enabled2:  new(any(true)),
					disabled2: new(any(false)),
				},
				DefaultRule: &flag.Rule{
					ProgressiveRollout: &flag.ProgressiveRollout{
						Initial: &flag.ProgressiveRolloutStep{
							Variation:  new(disabled2),
							Percentage: new(float64(0)),
						},
						End: &flag.ProgressiveRolloutStep{
							Variation:  new(enabled2),
							Percentage: new(float64(100)),
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "Should not require bucketing - only targeting queries without percentages",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					enabled2:  new(any(true)),
					disabled2: new(any(false)),
				},
				Rules: &[]flag.Rule{
					{
						Query:           new(keyEqAdmin),
						VariationResult: new(enabled2),
					},
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(disabled2),
				},
			},
			expected: false,
		},
		{
			name: "Should require bucketing - scheduled rollout with percentages",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					enabled2:  new(any(true)),
					disabled2: new(any(false)),
				},
				Rules: &[]flag.Rule{
					{
						Query:           new(keyEqAdmin),
						VariationResult: new(enabled2),
					},
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(disabled2),
				},
				Scheduled: &[]flag.ScheduledStep{
					{
						DefaultRule: &flag.Rule{
							Percentages: &map[string]float64{
								enabled2:  50,
								disabled2: 50,
							},
						},
						Date: new(fixedNow().Add(-5 * time.Second)),
					},
				},
			},
			expected: true,
		},
		{
			name: "Should require bucketing - scheduled rollout with progressive rollout",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					enabled2:  new(any(true)),
					disabled2: new(any(false)),
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(disabled2),
				},
				Scheduled: &[]flag.ScheduledStep{
					{
						DefaultRule: &flag.Rule{
							ProgressiveRollout: &flag.ProgressiveRollout{
								Initial: &flag.ProgressiveRolloutStep{
									Variation:  new(disabled2),
									Percentage: new(float64(0)),
									Date:       new(fixedNow()),
								},
								End: &flag.ProgressiveRolloutStep{
									Variation:  new(enabled2),
									Percentage: new(float64(100)),
									Date:       new(fixedNow().Add(time.Hour)),
								},
							},
						},
						Date: new(fixedNow()),
					},
				},
			},
			expected: true,
		},
		{
			name: "Should require bucketing - scheduled rollout with targeting rule percentages",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					enabled2:  new(any(true)),
					disabled2: new(any(false)),
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(disabled2),
				},
				Scheduled: &[]flag.ScheduledStep{
					{
						Rules: &[]flag.Rule{
							{
								Query: new("beta eq true"),
								Percentages: &map[string]float64{
									enabled2:  25,
									disabled2: 75,
								},
							},
						},
						Date: new(fixedNow()),
					},
				},
			},
			expected: true,
		},
		{
			name: "Should not require bucketing - scheduled rollout with only static variations",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					enabled2:  new(any(true)),
					disabled2: new(any(false)),
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(disabled2),
				},
				Scheduled: &[]flag.ScheduledStep{
					{
						DefaultRule: &flag.Rule{
							VariationResult: new(enabled2),
						},
						Date: new(fixedNow()),
					},
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := tt.flag.RequiresBucketing()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestInternalFlag_Value_EmptyContext(t *testing.T) {
	t.Parallel()

	tests := []struct {
		flag              flag.InternalFlag
		evaluationCtx     ffcontext.Context
		expectedValue     any
		name              string
		expectedErrorCode string
		expectedReason    string
		description       string
		shouldSucceed     bool
	}{
		{
			name: "Should succeed with empty context for static flag",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					enabled2:  new(any(true)),
					disabled2: new(any(false)),
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(disabled2),
				},
			},
			evaluationCtx:  ffcontext.NewEvaluationContext(""),
			expectedValue:  false,
			expectedReason: flag.ReasonStatic,
			shouldSucceed:  true,
			description:    "Flag without bucketing requirements should work with empty context",
		},
		{
			name: "Should fail with empty context for percentage-based flag",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					enabled2:  new(any(true)),
					disabled2: new(any(false)),
				},
				DefaultRule: &flag.Rule{
					Percentages: &map[string]float64{
						enabled2:  20,
						disabled2: 80,
					},
				},
			},
			evaluationCtx:     ffcontext.NewEvaluationContext(""),
			expectedErrorCode: flag.ErrorCodeTargetingKeyMissing,
			expectedReason:    flag.ReasonError,
			shouldSucceed:     false,
			description:       "Flag with percentage-based rollout should fail with empty context",
		},
		{
			name: "Should succeed with targeting key for percentage-based flag",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					enabled2:  new(any(true)),
					disabled2: new(any(false)),
				},
				DefaultRule: &flag.Rule{
					Percentages: &map[string]float64{
						enabled2:  20,
						disabled2: 80,
					},
				},
			},
			evaluationCtx:  ffcontext.NewEvaluationContext(user123),
			expectedReason: flag.ReasonSplit,
			shouldSucceed:  true,
			description:    "Flag with percentage-based rollout should work with targeting key",
		},
		{
			name: "Should succeed with empty context for targeting rule without percentages",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					enabled2:  new(any(true)),
					disabled2: new(any(false)),
				},
				Rules: &[]flag.Rule{
					{
						Query:           new(anonymousEqTrue),
						VariationResult: new(enabled2),
					},
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(disabled2),
				},
			},
			evaluationCtx: func() ffcontext.Context {
				ctx := ffcontext.NewEvaluationContext("")
				ctx.AddCustomAttribute(anonymous, true)

				return ctx
			}(),
			expectedValue:  true,
			expectedReason: flag.ReasonTargetingMatch,
			shouldSucceed:  true,
			description:    "Flag with targeting rule but no bucketing should work with empty context",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			flagContext := flag.Context{Service: flag.NewService(), DefaultSdkValue: false}
			value, resolutionDetails := tt.flag.Value(testFlag, atFixedNow(tt.evaluationCtx), flagContext)

			if tt.shouldSucceed {
				assert.Empty(t, resolutionDetails.ErrorCode, tt.description)
				assert.Equal(t, tt.expectedReason, resolutionDetails.Reason, tt.description)

				if tt.expectedValue != nil {
					assert.Equal(t, tt.expectedValue, value, tt.description)
				}
			} else {
				assert.Equal(t, tt.expectedErrorCode, resolutionDetails.ErrorCode, tt.description)
				assert.Equal(t, tt.expectedReason, resolutionDetails.Reason, tt.description)
			}
		})
	}
}

func TestInternalFlag_Value_NilContext(t *testing.T) {
	t.Parallel()

	tests := []struct {
		flag              flag.InternalFlag
		evaluationCtx     ffcontext.Context
		expectedValue     any
		name              string
		expectedErrorCode string
		expectedReason    string
		description       string
		shouldSucceed     bool
	}{
		{
			name: "Should succeed with nil context for static flag",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					enabled2:  new(any(true)),
					disabled2: new(any(false)),
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(disabled2),
				},
			},
			evaluationCtx:  nil,
			expectedValue:  false,
			expectedReason: flag.ReasonStatic,
			shouldSucceed:  true,
			description:    "Flag without bucketing requirements should work with nil context",
		},
		{
			name: "Should fail with nil context for percentage-based flag",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					enabled2:  new(any(true)),
					disabled2: new(any(false)),
				},
				DefaultRule: &flag.Rule{
					Percentages: &map[string]float64{
						enabled2:  20,
						disabled2: 80,
					},
				},
			},
			evaluationCtx:     nil,
			expectedErrorCode: flag.ErrorCodeTargetingKeyMissing,
			expectedReason:    flag.ReasonError,
			shouldSucceed:     false,
			description:       "Flag with percentage-based rollout should fail with nil context",
		},
		{
			name: "Should succeed with nil context for flag with targeting rule without percentages",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					enabled2:  new(any(true)),
					disabled2: new(any(false)),
				},
				Rules: &[]flag.Rule{
					{
						Query:           new(keyEqAdmin),
						VariationResult: new(enabled2),
					},
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(disabled2),
				},
			},
			evaluationCtx:  nil,
			expectedValue:  false,
			expectedReason: flag.ReasonDefault,
			shouldSucceed:  true,
			description:    "Flag with targeting rule but no bucketing should work with nil context (falls back to default)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			flagContext := flag.Context{Service: flag.NewService(), DefaultSdkValue: false}
			value, resolutionDetails := tt.flag.Value(testFlag, atFixedNow(tt.evaluationCtx), flagContext)

			if tt.shouldSucceed {
				assert.Empty(t, resolutionDetails.ErrorCode, tt.description)
				assert.Equal(t, tt.expectedReason, resolutionDetails.Reason, tt.description)

				if tt.expectedValue != nil {
					assert.Equal(t, tt.expectedValue, value, tt.description)
				}
			} else {
				assert.Equal(t, tt.expectedErrorCode, resolutionDetails.ErrorCode, tt.description)
				assert.Equal(t, tt.expectedReason, resolutionDetails.Reason, tt.description)
			}
		})
	}
}

func TestInternalFlag_GetBucketingKeyValue_EmptyContext(t *testing.T) {
	t.Parallel()

	tests := []struct {
		flag          flag.InternalFlag
		evaluationCtx ffcontext.Context
		name          string
		expectedKey   string
		description   string
		expectedError bool
	}{
		{
			name: "Should allow empty key for non-bucketing flag",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					enabled2:  new(any(true)),
					disabled2: new(any(false)),
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(disabled2),
				},
			},
			evaluationCtx: ffcontext.NewEvaluationContext(""),
			expectedKey:   "",
			expectedError: false,
			description:   "Non-bucketing flag should allow empty targeting key",
		},
		{
			name: "Should require key for bucketing flag",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					enabled2:  new(any(true)),
					disabled2: new(any(false)),
				},
				DefaultRule: &flag.Rule{
					Percentages: &map[string]float64{
						enabled2:  50,
						disabled2: 50,
					},
				},
			},
			evaluationCtx: ffcontext.NewEvaluationContext(""),
			expectedKey:   "",
			expectedError: true,
			description:   "Bucketing flag should require targeting key",
		},
		{
			name: "Should use custom bucketing key when provided",
			flag: flag.InternalFlag{
				BucketingKey: new(teamID),
				Variations: &map[string]*any{
					enabled2:  new(any(true)),
					disabled2: new(any(false)),
				},
				DefaultRule: &flag.Rule{
					Percentages: &map[string]float64{
						enabled2:  50,
						disabled2: 50,
					},
				},
			},
			evaluationCtx: func() ffcontext.Context {
				ctx := ffcontext.NewEvaluationContext("")
				ctx.AddCustomAttribute(teamID, team123)

				return ctx
			}(),
			expectedKey:   team123,
			expectedError: false,
			description:   "Should use custom bucketing key when available",
		},
		{
			name: "Should allow empty custom bucketing key for non-bucketing flag",
			flag: flag.InternalFlag{
				BucketingKey: new(teamID),
				Variations: &map[string]*any{
					enabled2:  new(any(true)),
					disabled2: new(any(false)),
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(disabled2),
				},
			},
			evaluationCtx: func() ffcontext.Context {
				ctx := ffcontext.NewEvaluationContext("")
				ctx.AddCustomAttribute(teamID, "")

				return ctx
			}(),
			expectedKey:   "",
			expectedError: false,
			description:   "Non-bucketing flag should allow empty custom bucketing key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			key, err := tt.flag.GetBucketingKeyValue(tt.evaluationCtx)

			if tt.expectedError {
				assert.Error(t, err, tt.description)
			} else {
				require.NoError(t, err, tt.description)
				assert.Equal(t, tt.expectedKey, key, tt.description)
			}
		})
	}
}

package flag_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/flag"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

const (
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
					fixtures.VariationEnabled:  new(any(true)),
					fixtures.VariationDisabled: new(any(false)),
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(fixtures.VariationDisabled),
				},
			},
			expected: false,
		},
		{
			name: "Should require bucketing - default rule has percentages",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					fixtures.VariationEnabled:  new(any(true)),
					fixtures.VariationDisabled: new(any(false)),
				},
				DefaultRule: &flag.Rule{
					Percentages: &map[string]float64{
						fixtures.VariationEnabled:  20,
						fixtures.VariationDisabled: 80,
					},
				},
			},
			expected: true,
		},
		{
			name: "Should require bucketing - targeting rule has percentages",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					fixtures.VariationEnabled:  new(any(true)),
					fixtures.VariationDisabled: new(any(false)),
				},
				Rules: &[]flag.Rule{
					{
						Query: new(keyEqAdmin),
						Percentages: &map[string]float64{
							fixtures.VariationEnabled:  50,
							fixtures.VariationDisabled: 50,
						},
					},
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(fixtures.VariationDisabled),
				},
			},
			expected: true,
		},
		{
			name: "Should require bucketing - has progressive rollout",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					fixtures.VariationEnabled:  new(any(true)),
					fixtures.VariationDisabled: new(any(false)),
				},
				DefaultRule: &flag.Rule{
					ProgressiveRollout: &flag.ProgressiveRollout{
						Initial: &flag.ProgressiveRolloutStep{
							Variation:  new(fixtures.VariationDisabled),
							Percentage: new(float64(0)),
						},
						End: &flag.ProgressiveRolloutStep{
							Variation:  new(fixtures.VariationEnabled),
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
					fixtures.VariationEnabled:  new(any(true)),
					fixtures.VariationDisabled: new(any(false)),
				},
				Rules: &[]flag.Rule{
					{
						Query:           new(keyEqAdmin),
						VariationResult: new(fixtures.VariationEnabled),
					},
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(fixtures.VariationDisabled),
				},
			},
			expected: false,
		},
		{
			name: "Should require bucketing - scheduled rollout with percentages",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					fixtures.VariationEnabled:  new(any(true)),
					fixtures.VariationDisabled: new(any(false)),
				},
				Rules: &[]flag.Rule{
					{
						Query:           new(keyEqAdmin),
						VariationResult: new(fixtures.VariationEnabled),
					},
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(fixtures.VariationDisabled),
				},
				Scheduled: &[]flag.ScheduledStep{
					{
						DefaultRule: &flag.Rule{
							Percentages: &map[string]float64{
								fixtures.VariationEnabled:  50,
								fixtures.VariationDisabled: 50,
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
					fixtures.VariationEnabled:  new(any(true)),
					fixtures.VariationDisabled: new(any(false)),
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(fixtures.VariationDisabled),
				},
				Scheduled: &[]flag.ScheduledStep{
					{
						DefaultRule: &flag.Rule{
							ProgressiveRollout: &flag.ProgressiveRollout{
								Initial: &flag.ProgressiveRolloutStep{
									Variation:  new(fixtures.VariationDisabled),
									Percentage: new(float64(0)),
									Date:       new(fixedNow()),
								},
								End: &flag.ProgressiveRolloutStep{
									Variation:  new(fixtures.VariationEnabled),
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
					fixtures.VariationEnabled:  new(any(true)),
					fixtures.VariationDisabled: new(any(false)),
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(fixtures.VariationDisabled),
				},
				Scheduled: &[]flag.ScheduledStep{
					{
						Rules: &[]flag.Rule{
							{
								Query: new("beta eq true"),
								Percentages: &map[string]float64{
									fixtures.VariationEnabled:  25,
									fixtures.VariationDisabled: 75,
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
					fixtures.VariationEnabled:  new(any(true)),
					fixtures.VariationDisabled: new(any(false)),
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(fixtures.VariationDisabled),
				},
				Scheduled: &[]flag.ScheduledStep{
					{
						DefaultRule: &flag.Rule{
							VariationResult: new(fixtures.VariationEnabled),
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
					fixtures.VariationEnabled:  new(any(true)),
					fixtures.VariationDisabled: new(any(false)),
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(fixtures.VariationDisabled),
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
					fixtures.VariationEnabled:  new(any(true)),
					fixtures.VariationDisabled: new(any(false)),
				},
				DefaultRule: &flag.Rule{
					Percentages: &map[string]float64{
						fixtures.VariationEnabled:  20,
						fixtures.VariationDisabled: 80,
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
					fixtures.VariationEnabled:  new(any(true)),
					fixtures.VariationDisabled: new(any(false)),
				},
				DefaultRule: &flag.Rule{
					Percentages: &map[string]float64{
						fixtures.VariationEnabled:  20,
						fixtures.VariationDisabled: 80,
					},
				},
			},
			evaluationCtx:  ffcontext.NewEvaluationContext(fixtures.UserKeyNumbered),
			expectedReason: flag.ReasonSplit,
			shouldSucceed:  true,
			description:    "Flag with percentage-based rollout should work with targeting key",
		},
		{
			name: "Should succeed with empty context for targeting rule without percentages",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					fixtures.VariationEnabled:  new(any(true)),
					fixtures.VariationDisabled: new(any(false)),
				},
				Rules: &[]flag.Rule{
					{
						Query:           new(fixtures.AnonymousEqTrue),
						VariationResult: new(fixtures.VariationEnabled),
					},
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(fixtures.VariationDisabled),
				},
			},
			evaluationCtx: func() ffcontext.Context {
				ctx := ffcontext.NewEvaluationContext("")
				ctx.AddCustomAttribute(fixtures.Anonymous, true)

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
			value, resolutionDetails := tt.flag.Value(fixtures.FlagKeyTest, atFixedNow(tt.evaluationCtx), flagContext)

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
					fixtures.VariationEnabled:  new(any(true)),
					fixtures.VariationDisabled: new(any(false)),
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(fixtures.VariationDisabled),
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
					fixtures.VariationEnabled:  new(any(true)),
					fixtures.VariationDisabled: new(any(false)),
				},
				DefaultRule: &flag.Rule{
					Percentages: &map[string]float64{
						fixtures.VariationEnabled:  20,
						fixtures.VariationDisabled: 80,
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
					fixtures.VariationEnabled:  new(any(true)),
					fixtures.VariationDisabled: new(any(false)),
				},
				Rules: &[]flag.Rule{
					{
						Query:           new(keyEqAdmin),
						VariationResult: new(fixtures.VariationEnabled),
					},
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(fixtures.VariationDisabled),
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
			value, resolutionDetails := tt.flag.Value(fixtures.FlagKeyTest, atFixedNow(tt.evaluationCtx), flagContext)

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
					fixtures.VariationEnabled:  new(any(true)),
					fixtures.VariationDisabled: new(any(false)),
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(fixtures.VariationDisabled),
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
					fixtures.VariationEnabled:  new(any(true)),
					fixtures.VariationDisabled: new(any(false)),
				},
				DefaultRule: &flag.Rule{
					Percentages: &map[string]float64{
						fixtures.VariationEnabled:  50,
						fixtures.VariationDisabled: 50,
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
					fixtures.VariationEnabled:  new(any(true)),
					fixtures.VariationDisabled: new(any(false)),
				},
				DefaultRule: &flag.Rule{
					Percentages: &map[string]float64{
						fixtures.VariationEnabled:  50,
						fixtures.VariationDisabled: 50,
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
					fixtures.VariationEnabled:  new(any(true)),
					fixtures.VariationDisabled: new(any(false)),
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(fixtures.VariationDisabled),
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

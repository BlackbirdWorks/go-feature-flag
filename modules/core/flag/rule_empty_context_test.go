package flag_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/flag"
)

const (
	user123 = "user-123"
)

func TestRule_RequiresBucketing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		rule     flag.Rule
		expected bool
	}{
		{
			name: "Should not require bucketing - static variation",
			rule: flag.Rule{
				VariationResult: new(enabled2),
			},
			expected: false,
		},
		{
			name: "Should require bucketing - has percentages",
			rule: flag.Rule{
				Percentages: &map[string]float64{
					enabled2:  50,
					disabled2: 50,
				},
			},
			expected: true,
		},
		{
			name: "Should not require bucketing - empty percentages",
			rule: flag.Rule{
				Percentages: &map[string]float64{},
			},
			expected: false,
		},
		{
			name: "Should require bucketing - has progressive rollout",
			rule: flag.Rule{
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
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := tt.rule.RequiresBucketing()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRule_Evaluate_EmptyKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		ctx           ffcontext.Context
		rule          flag.Rule
		name          string
		key           string
		expectedValue string
		description   string
		expectedError bool
	}{
		{
			name: "Should succeed with empty key for static variation",
			rule: flag.Rule{
				VariationResult: new(enabled2),
			},
			key:           "",
			ctx:           ffcontext.NewEvaluationContext(""),
			expectedError: false,
			expectedValue: enabled2,
			description:   "Static variation should work without key",
		},
		{
			name: "Should fail with empty key for percentage rule",
			rule: flag.Rule{
				Percentages: &map[string]float64{
					enabled2:  50,
					disabled2: 50,
				},
			},
			key:           "",
			ctx:           ffcontext.NewEvaluationContext(""),
			expectedError: true,
			description:   "Percentage rule should fail without key",
		},
		{
			name: "Should succeed with key for percentage rule",
			rule: flag.Rule{
				Percentages: &map[string]float64{
					enabled2:  50,
					disabled2: 50,
				},
			},
			key:           user123,
			ctx:           ffcontext.NewEvaluationContext(user123),
			expectedError: false,
			description:   "Percentage rule should work with key",
		},
		{
			name: "Should fail with empty key for progressive rollout",
			rule: flag.Rule{
				ProgressiveRollout: &flag.ProgressiveRollout{
					Initial: &flag.ProgressiveRolloutStep{
						Variation:  new(disabled2),
						Percentage: new(float64(0)),
						Date:       new(fixedNow().Add(-time.Hour)),
					},
					End: &flag.ProgressiveRolloutStep{
						Variation:  new(enabled2),
						Percentage: new(float64(100)),
						Date:       new(fixedNow().Add(time.Hour)),
					},
				},
			},
			key:           "",
			ctx:           ffcontext.NewEvaluationContext(""),
			expectedError: true,
			description:   "Progressive rollout should fail without key",
		},
		{
			name: "Should succeed with empty key for query-only rule",
			rule: flag.Rule{
				Query:           new(anonymousEqTrue),
				VariationResult: new(enabled2),
			},
			key: "",
			ctx: func() ffcontext.Context {
				ctx := ffcontext.NewEvaluationContext("")
				ctx.AddCustomAttribute(anonymous, true)

				return ctx
			}(),
			expectedError: false,
			expectedValue: enabled2,
			description:   "Query-only rule should work without key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result, err := tt.rule.Evaluate(flag.NewService(), tt.key, atFixedNow(tt.ctx), testFlag, false)

			if tt.expectedError {
				assert.Error(t, err, tt.description)
			} else {
				assert.NoError(t, err, tt.description)

				if tt.expectedValue != "" {
					assert.Equal(t, tt.expectedValue, result, tt.description)
				}
			}
		})
	}
}

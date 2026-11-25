package flagdef_test

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
)

const (
	invalidProgressiveRolloutInitial = "invalid progressive rollout, initial and end steps are required"
	invalidQuery                     = "invalid query"
	invalidQuerySyntax               = "invalid query syntax"
	isValidErrorV                    = "IsValid() error = %v"
	keyEqTest                        = "key eq \"test\""
	nonExistent                      = "non_existent"
)

type validateProgressiveRolloutCase struct {
	variations  map[string]*any
	wantErr     assert.ErrorAssertionFunc
	rule        flagdef.Rule
	name        string
	wantErrMsg  string
	defaultRule bool
}

func TestRule_IsValid(t *testing.T) {
	t.Parallel()

	variations := map[string]*any{
		variationA: new(any(valueAUnderscore)),
		variationB: new(any(valueBUnderscore)),
		variationC: new(any(valueC)),
	}

	tests := []struct {
		variations  map[string]*any
		wantErr     assert.ErrorAssertionFunc
		rule        flagdef.Rule
		name        string
		defaultRule bool
	}{
		{
			name: "valid rule with variation result",
			rule: flagdef.Rule{
				Query:           new(keyEqTest),
				VariationResult: new(variationA),
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.NoError,
		},
		{
			name: "valid rule with percentages",
			rule: flagdef.Rule{
				Query: new(keyEqTest),
				Percentages: &map[string]float64{
					variationA: 50,
					variationB: 50,
				},
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.NoError,
		},
		{
			name: "valid rule with progressive rollout",
			rule: flagdef.Rule{
				Query: new(keyEqTest),
				ProgressiveRollout: &flagdef.ProgressiveRollout{
					Initial: &flagdef.ProgressiveRolloutStep{
						Variation:  new(variationA),
						Percentage: new(float64(0)),
						Date:       new(fixedNow().Add(1 * time.Hour)),
					},
					End: &flagdef.ProgressiveRolloutStep{
						Variation:  new(variationB),
						Percentage: new(float64(100)),
						Date:       new(fixedNow().Add(2 * time.Hour)),
					},
				},
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.NoError,
		},
		{
			name: "disabled rule should be valid",
			rule: flagdef.Rule{
				Disable: new(true),
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.NoError,
		},
		{
			name: "disabled rule with defaultRule should be valid",
			rule: flagdef.Rule{
				Disable:         new(true),
				VariationResult: new(variationA),
			},
			defaultRule: true,
			variations:  variations,
			wantErr:     assert.NoError,
		},
		{
			name: "rule with no return value",
			rule: flagdef.Rule{
				Query: new(keyEqTest),
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.Error,
		},
		{
			name: "valid default rule without query",
			rule: flagdef.Rule{
				VariationResult: new(variationA),
			},
			defaultRule: true,
			variations:  variations,
			wantErr:     assert.NoError,
		},
		{
			name: "non-default rule without query should fail",
			rule: flagdef.Rule{
				VariationResult: new(variationA),
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.Error,
		},
		{
			name: "rule with invalid query",
			rule: flagdef.Rule{
				VariationResult: new(variationA),
				Query:           new(invalidQuerySyntax),
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.Error,
		},
		{
			name: "rule with invalid percentages",
			rule: flagdef.Rule{
				Query: new(keyEqTest),
				Percentages: &map[string]float64{
					nonExistent: 100,
				},
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.Error,
		},
		{
			name: "rule with invalid progressive rollout",
			rule: flagdef.Rule{
				Query: new(keyEqTest),
				ProgressiveRollout: &flagdef.ProgressiveRollout{
					Initial: &flagdef.ProgressiveRolloutStep{
						Variation:  new(variationA),
						Percentage: new(float64(100)),
						Date:       new(fixedNow().Add(1 * time.Hour)),
					},
					End: &flagdef.ProgressiveRolloutStep{
						Variation:  new(variationB),
						Percentage: new(float64(0)),
						Date:       new(fixedNow().Add(2 * time.Hour)),
					},
				},
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.Error,
		},
		{
			name: "rule with invalid variation result",
			rule: flagdef.Rule{
				VariationResult: new(nonExistent),
			},
			defaultRule: true,
			variations:  variations,
			wantErr:     assert.Error,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.rule.IsValid(flagdef.NewService(), tt.defaultRule, tt.variations)
			tt.wantErr(t, err, isValidErrorV, err)
		})
	}
}

func TestRule_validatePercentages(t *testing.T) {
	t.Parallel()

	variations := map[string]*any{
		variationA: new(any(valueAUnderscore)),
		variationB: new(any(valueBUnderscore)),
		variationC: new(any(valueC)),
	}

	tests := []struct {
		variations  map[string]*any
		wantErr     assert.ErrorAssertionFunc
		rule        flagdef.Rule
		name        string
		wantErrMsg  string
		defaultRule bool
	}{
		{
			name: "nil percentages should be valid",
			rule: flagdef.Rule{
				Query:           new(keyEqTest),
				VariationResult: new(variationA),
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.NoError,
		},
		{
			name: "valid percentages",
			rule: flagdef.Rule{
				Query: new(keyEqTest),
				Percentages: &map[string]float64{
					variationA: 30,
					variationB: 70,
				},
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.NoError,
		},
		{
			name: "valid percentages summing to 100",
			rule: flagdef.Rule{
				Query: new(keyEqTest),
				Percentages: &map[string]float64{
					variationA: 25,
					variationB: 25,
					variationC: 50,
				},
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.NoError,
		},
		{
			name: "empty percentages should fail",
			rule: flagdef.Rule{
				Query:       new(keyEqTest),
				Percentages: &map[string]float64{},
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.Error,
			wantErrMsg:  invalidPercentagesShouldNot,
		},
		{
			name: "percentages summing to zero should fail",
			rule: flagdef.Rule{
				Query: new(keyEqTest),
				Percentages: &map[string]float64{
					variationA: 0,
					variationB: 0,
				},
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.Error,
			wantErrMsg:  invalidPercentagesShouldNotBe,
		},
		{
			name: "percentages with non-existent variation should fail",
			rule: flagdef.Rule{
				Query: new(keyEqTest),
				Percentages: &map[string]float64{
					nonExistent: 100,
				},
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.Error,
			wantErrMsg:  "invalid percentage: variation non_existent does not exist",
		},
		{
			name: "percentages with multiple non-existent variations should fail",
			rule: flagdef.Rule{
				Query: new(keyEqTest),
				Percentages: &map[string]float64{
					variationA:       50,
					"non_existent_1": 25,
					"non_existent_2": 25,
				},
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.Error,
		},
		{
			name: "percentages with negative values should fail even when the sum is positive",
			rule: flagdef.Rule{
				Query: new(keyEqTest),
				Percentages: &map[string]float64{
					variationA: -10,
					variationB: 110,
				},
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.Error,
			wantErrMsg:  "invalid percentage: variation variation_A has a negative or non-finite percentage (-10)",
		},
		{
			name: "percentages with NaN should fail",
			rule: flagdef.Rule{
				Query: new(keyEqTest),
				Percentages: &map[string]float64{
					variationA: math.NaN(),
					variationB: 100,
				},
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.Error,
			wantErrMsg:  "invalid percentage: variation variation_A has a negative or non-finite percentage (NaN)",
		},
		{
			name: "percentages with positive infinity should fail",
			rule: flagdef.Rule{
				Query: new(keyEqTest),
				Percentages: &map[string]float64{
					variationA: math.Inf(1),
				},
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.Error,
			wantErrMsg:  "invalid percentage: variation variation_A has a negative or non-finite percentage (+Inf)",
		},
		{
			name: "percentages too large to be scaled should fail",
			rule: flagdef.Rule{
				Query: new(keyEqTest),
				Percentages: &map[string]float64{
					variationA: math.MaxFloat64,
				},
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.Error,
			wantErrMsg:  "invalid percentages: the sum (",
		},
		{
			name: "percentages summing to more than 100 should be valid",
			rule: flagdef.Rule{
				Query: new(keyEqTest),
				Percentages: &map[string]float64{
					variationA: 150,
					variationB: 50,
				},
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.NoError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.rule.IsValid(flagdef.NewService(), tt.defaultRule, tt.variations)
			tt.wantErr(t, err, isValidErrorV, err)

			if tt.wantErrMsg != "" && err != nil {
				assert.Contains(t, err.Error(), tt.wantErrMsg)
			}
		})
	}
}

func runValidateProgressiveRolloutTests(t *testing.T, tests []validateProgressiveRolloutCase) {
	t.Helper()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.rule.IsValid(flagdef.NewService(), tt.defaultRule, tt.variations)
			tt.wantErr(t, err, isValidErrorV, err)

			if tt.wantErrMsg != "" && err != nil {
				assert.Contains(t, err.Error(), tt.wantErrMsg)
			}
		})
	}
}

func TestRule_validateProgressiveRollout(t *testing.T) {
	t.Parallel()

	variations := map[string]*any{
		variationA: new(any(valueAUnderscore)),
		variationB: new(any(valueBUnderscore)),
		variationC: new(any(valueC)),
	}

	runValidateProgressiveRolloutTests(t, []validateProgressiveRolloutCase{
		{
			name: "nil progressive rollout should be valid",
			rule: flagdef.Rule{
				Query:           new(keyEqTest),
				VariationResult: new(variationA),
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.NoError,
		},
		{
			name: "valid progressive rollout",
			rule: flagdef.Rule{
				Query: new(keyEqTest),
				ProgressiveRollout: &flagdef.ProgressiveRollout{
					Initial: &flagdef.ProgressiveRolloutStep{
						Variation:  new(variationA),
						Percentage: new(float64(0)),
						Date:       new(fixedNow().Add(1 * time.Hour)),
					},
					End: &flagdef.ProgressiveRolloutStep{
						Variation:  new(variationB),
						Percentage: new(float64(100)),
						Date:       new(fixedNow().Add(2 * time.Hour)),
					},
				},
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.NoError,
		},
		{
			name: "valid progressive rollout with equal percentages",
			rule: flagdef.Rule{
				Query: new(keyEqTest),
				ProgressiveRollout: &flagdef.ProgressiveRollout{
					Initial: &flagdef.ProgressiveRolloutStep{
						Variation:  new(variationA),
						Percentage: new(float64(50)),
						Date:       new(fixedNow().Add(1 * time.Hour)),
					},
					End: &flagdef.ProgressiveRolloutStep{
						Variation:  new(variationB),
						Percentage: new(float64(50)),
						Date:       new(fixedNow().Add(2 * time.Hour)),
					},
				},
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.NoError,
		},
		{
			name: "progressive rollout with initial percentage higher than end should fail",
			rule: flagdef.Rule{
				Query: new(keyEqTest),
				ProgressiveRollout: &flagdef.ProgressiveRollout{
					Initial: &flagdef.ProgressiveRolloutStep{
						Variation:  new(variationA),
						Percentage: new(float64(100)),
						Date:       new(fixedNow().Add(1 * time.Hour)),
					},
					End: &flagdef.ProgressiveRolloutStep{
						Variation:  new(variationB),
						Percentage: new(float64(0)),
						Date:       new(fixedNow().Add(2 * time.Hour)),
					},
				},
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.Error,
			wantErrMsg:  "invalid progressive rollout, initial percentage should be lower",
		},
		{
			name: "progressive rollout with non-existent end variation should fail",
			rule: flagdef.Rule{
				Query: new(keyEqTest),
				ProgressiveRollout: &flagdef.ProgressiveRollout{
					Initial: &flagdef.ProgressiveRolloutStep{
						Variation:  new(variationA),
						Percentage: new(float64(0)),
						Date:       new(fixedNow().Add(1 * time.Hour)),
					},
					End: &flagdef.ProgressiveRolloutStep{
						Variation:  new(nonExistent),
						Percentage: new(float64(100)),
						Date:       new(fixedNow().Add(2 * time.Hour)),
					},
				},
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.Error,
			wantErrMsg:  "invalid progressive rollout, end variation non_existent does not exist",
		},
		{
			name: "progressive rollout with non-existent initial variation should fail",
			rule: flagdef.Rule{
				Query: new(keyEqTest),
				ProgressiveRollout: &flagdef.ProgressiveRollout{
					Initial: &flagdef.ProgressiveRolloutStep{
						Variation:  new(nonExistent),
						Percentage: new(float64(0)),
						Date:       new(fixedNow().Add(1 * time.Hour)),
					},
					End: &flagdef.ProgressiveRolloutStep{
						Variation:  new(variationB),
						Percentage: new(float64(100)),
						Date:       new(fixedNow().Add(2 * time.Hour)),
					},
				},
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.Error,
			wantErrMsg:  "invalid progressive rollout, initial variation non_existent does not exist",
		},
		{
			name: "progressive rollout with nil initial step should fail",
			rule: flagdef.Rule{
				Query: new(keyEqTest),
				ProgressiveRollout: &flagdef.ProgressiveRollout{
					End: &flagdef.ProgressiveRolloutStep{
						Variation:  new(variationB),
						Percentage: new(float64(100)),
						Date:       new(fixedNow().Add(2 * time.Hour)),
					},
				},
			},
			variations: variations,
			wantErr:    assert.Error,
			wantErrMsg: invalidProgressiveRolloutInitial,
		},
		{
			name: "progressive rollout with nil end step should fail",
			rule: flagdef.Rule{
				Query: new(keyEqTest),
				ProgressiveRollout: &flagdef.ProgressiveRollout{
					Initial: &flagdef.ProgressiveRolloutStep{
						Variation:  new(variationA),
						Percentage: new(float64(0)),
						Date:       new(fixedNow().Add(1 * time.Hour)),
					},
				},
			},
			variations: variations,
			wantErr:    assert.Error,
			wantErrMsg: invalidProgressiveRolloutInitial,
		},
		{
			name: "empty progressive rollout should fail",
			rule: flagdef.Rule{
				Query:              new(keyEqTest),
				ProgressiveRollout: &flagdef.ProgressiveRollout{},
			},
			variations: variations,
			wantErr:    assert.Error,
			wantErrMsg: invalidProgressiveRolloutInitial,
		},
	})
}

func TestRule_validateProgressiveRollout_ErrorsAndDefaults(t *testing.T) {
	t.Parallel()

	variations := map[string]*any{
		variationA: new(any(valueAUnderscore)),
		variationB: new(any(valueBUnderscore)),
		variationC: new(any(valueC)),
	}

	runValidateProgressiveRolloutTests(t, []validateProgressiveRolloutCase{
		{
			name: "progressive rollout with negative initial percentage should fail",
			rule: flagdef.Rule{
				Query: new(keyEqTest),
				ProgressiveRollout: &flagdef.ProgressiveRollout{
					Initial: &flagdef.ProgressiveRolloutStep{
						Variation:  new(variationA),
						Percentage: new(float64(-50)),
						Date:       new(fixedNow().Add(1 * time.Hour)),
					},
					End: &flagdef.ProgressiveRolloutStep{
						Variation:  new(variationB),
						Percentage: new(float64(100)),
						Date:       new(fixedNow().Add(2 * time.Hour)),
					},
				},
			},
			variations: variations,
			wantErr:    assert.Error,
			wantErrMsg: "invalid percentage: initial step percentage has a negative or non-finite percentage (-50)",
		},
		{
			name: "progressive rollout with NaN initial percentage should fail",
			rule: flagdef.Rule{
				Query: new(keyEqTest),
				ProgressiveRollout: &flagdef.ProgressiveRollout{
					Initial: &flagdef.ProgressiveRolloutStep{
						Variation:  new(variationA),
						Percentage: new(math.NaN()),
						Date:       new(fixedNow().Add(1 * time.Hour)),
					},
					End: &flagdef.ProgressiveRolloutStep{
						Variation:  new(variationB),
						Percentage: new(float64(100)),
						Date:       new(fixedNow().Add(2 * time.Hour)),
					},
				},
			},
			variations: variations,
			wantErr:    assert.Error,
			wantErrMsg: "invalid percentage: initial step percentage has a negative or non-finite percentage (NaN)",
		},
		{
			name: "progressive rollout with NaN end percentage should fail",
			rule: flagdef.Rule{
				Query: new(keyEqTest),
				ProgressiveRollout: &flagdef.ProgressiveRollout{
					Initial: &flagdef.ProgressiveRolloutStep{
						Variation:  new(variationA),
						Percentage: new(float64(0)),
						Date:       new(fixedNow().Add(1 * time.Hour)),
					},
					End: &flagdef.ProgressiveRolloutStep{
						Variation:  new(variationB),
						Percentage: new(math.NaN()),
						Date:       new(fixedNow().Add(2 * time.Hour)),
					},
				},
			},
			variations: variations,
			wantErr:    assert.Error,
			wantErrMsg: "invalid percentage: end step percentage has a negative or non-finite percentage (NaN)",
		},
		{
			name: "progressive rollout with infinite end percentage should fail",
			rule: flagdef.Rule{
				Query: new(keyEqTest),
				ProgressiveRollout: &flagdef.ProgressiveRollout{
					Initial: &flagdef.ProgressiveRolloutStep{
						Variation:  new(variationA),
						Percentage: new(float64(0)),
						Date:       new(fixedNow().Add(1 * time.Hour)),
					},
					End: &flagdef.ProgressiveRolloutStep{
						Variation:  new(variationB),
						Percentage: new(math.Inf(1)),
						Date:       new(fixedNow().Add(2 * time.Hour)),
					},
				},
			},
			variations: variations,
			wantErr:    assert.Error,
			wantErrMsg: "invalid percentage: end step percentage has a negative or non-finite percentage (+Inf)",
		},
		{
			name: "progressive rollout with nil initial variation should fail",
			rule: flagdef.Rule{
				Query: new(keyEqTest),
				ProgressiveRollout: &flagdef.ProgressiveRollout{
					Initial: &flagdef.ProgressiveRolloutStep{
						Variation:  nil,
						Percentage: new(float64(0)),
						Date:       new(fixedNow().Add(1 * time.Hour)),
					},
					End: &flagdef.ProgressiveRolloutStep{
						Variation:  new(variationB),
						Percentage: new(float64(100)),
						Date:       new(fixedNow().Add(2 * time.Hour)),
					},
				},
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.Error,
			wantErrMsg:  "invalid progressive rollout, initial variation",
		},
		{
			name: "progressive rollout with nil end variation should fail",
			rule: flagdef.Rule{
				Query: new(keyEqTest),
				ProgressiveRollout: &flagdef.ProgressiveRollout{
					Initial: &flagdef.ProgressiveRolloutStep{
						Variation:  new(variationA),
						Percentage: new(float64(0)),
						Date:       new(fixedNow().Add(1 * time.Hour)),
					},
					End: &flagdef.ProgressiveRolloutStep{
						Variation:  nil,
						Percentage: new(float64(100)),
						Date:       new(fixedNow().Add(2 * time.Hour)),
					},
				},
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.Error,
			wantErrMsg:  "invalid progressive rollout, end variation",
		},
		{
			name: "progressive rollout with empty initial variation should fail",
			rule: flagdef.Rule{
				Query: new(keyEqTest),
				ProgressiveRollout: &flagdef.ProgressiveRollout{
					Initial: &flagdef.ProgressiveRolloutStep{
						Variation:  new(""),
						Percentage: new(float64(0)),
						Date:       new(fixedNow().Add(1 * time.Hour)),
					},
					End: &flagdef.ProgressiveRolloutStep{
						Variation:  new(variationB),
						Percentage: new(float64(100)),
						Date:       new(fixedNow().Add(2 * time.Hour)),
					},
				},
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.Error,
			wantErrMsg:  "invalid progressive rollout, initial variation",
		},
		{
			name: "progressive rollout with empty end variation should fail",
			rule: flagdef.Rule{
				Query: new(keyEqTest),
				ProgressiveRollout: &flagdef.ProgressiveRollout{
					Initial: &flagdef.ProgressiveRolloutStep{
						Variation:  new(variationA),
						Percentage: new(float64(0)),
						Date:       new(fixedNow().Add(1 * time.Hour)),
					},
					End: &flagdef.ProgressiveRolloutStep{
						Variation:  new(""),
						Percentage: new(float64(100)),
						Date:       new(fixedNow().Add(2 * time.Hour)),
					},
				},
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.Error,
			wantErrMsg:  "invalid progressive rollout, end variation",
		},
		{
			name: "progressive rollout without percentage should use default 0",
			rule: flagdef.Rule{
				Query: new(keyEqTest),
				ProgressiveRollout: &flagdef.ProgressiveRollout{
					Initial: &flagdef.ProgressiveRolloutStep{
						Variation: new(variationA),
						Date:      new(fixedNow().Add(1 * time.Hour)),
					},
					End: &flagdef.ProgressiveRolloutStep{
						Variation:  new(variationB),
						Percentage: new(float64(100)),
						Date:       new(fixedNow().Add(2 * time.Hour)),
					},
				},
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.NoError,
		},
	})
}

func TestRule_validateVariationResult(t *testing.T) {
	t.Parallel()

	variations := map[string]*any{
		variationA: new(any(valueAUnderscore)),
		variationB: new(any(valueBUnderscore)),
		variationC: new(any(valueC)),
	}

	tests := []struct {
		variations  map[string]*any
		wantErr     assert.ErrorAssertionFunc
		rule        flagdef.Rule
		name        string
		wantErrMsg  string
		defaultRule bool
	}{
		{
			name: "nil variation result should be valid when percentages exist",
			rule: flagdef.Rule{
				Query: new(keyEqTest),
				Percentages: &map[string]float64{
					variationA: 100,
				},
				VariationResult: nil,
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.NoError,
		},
		{
			name: "nil variation result should be valid when progressive rollout exists",
			rule: flagdef.Rule{
				Query: new(keyEqTest),
				ProgressiveRollout: &flagdef.ProgressiveRollout{
					Initial: &flagdef.ProgressiveRolloutStep{
						Variation:  new(variationA),
						Percentage: new(float64(0)),
						Date:       new(fixedNow().Add(1 * time.Hour)),
					},
					End: &flagdef.ProgressiveRolloutStep{
						Variation:  new(variationB),
						Percentage: new(float64(100)),
						Date:       new(fixedNow().Add(2 * time.Hour)),
					},
				},
				VariationResult: nil,
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.NoError,
		},
		{
			name: "valid variation result",
			rule: flagdef.Rule{
				VariationResult: new(variationA),
			},
			defaultRule: true,
			variations:  variations,
			wantErr:     assert.NoError,
		},
		{
			name: "variation result with non-existent variation should fail",
			rule: flagdef.Rule{
				VariationResult: new(nonExistent),
			},
			defaultRule: true,
			variations:  variations,
			wantErr:     assert.Error,
			wantErrMsg:  "invalid variation: non_existent does not exist",
		},
		{
			name: "variation result with empty string should fail",
			rule: flagdef.Rule{
				VariationResult: new(""),
			},
			defaultRule: true,
			variations:  variations,
			wantErr:     assert.Error,
			wantErrMsg:  "invalid variation:",
		},
		{
			name: "variation result should be ignored when percentages exist",
			rule: flagdef.Rule{
				Query: new(keyEqTest),
				Percentages: &map[string]float64{
					variationA: 100,
				},
				VariationResult: new(nonExistent),
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.NoError,
		},
		{
			name: "variation result should be ignored when progressive rollout exists",
			rule: flagdef.Rule{
				Query: new(keyEqTest),
				ProgressiveRollout: &flagdef.ProgressiveRollout{
					Initial: &flagdef.ProgressiveRolloutStep{
						Variation:  new(variationA),
						Percentage: new(float64(0)),
						Date:       new(fixedNow().Add(1 * time.Hour)),
					},
					End: &flagdef.ProgressiveRolloutStep{
						Variation:  new(variationB),
						Percentage: new(float64(100)),
						Date:       new(fixedNow().Add(2 * time.Hour)),
					},
				},
				VariationResult: new(nonExistent),
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.NoError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.rule.IsValid(flagdef.NewService(), tt.defaultRule, tt.variations)
			tt.wantErr(t, err, isValidErrorV, err)

			if tt.wantErrMsg != "" && err != nil {
				assert.Contains(t, err.Error(), tt.wantErrMsg)
			}
		})
	}
}

func TestRule_isQueryValid(t *testing.T) {
	t.Parallel()

	variations := map[string]*any{
		variationA: new(any(valueAUnderscore)),
	}

	tests := []struct {
		variations  map[string]*any
		wantErr     assert.ErrorAssertionFunc
		rule        flagdef.Rule
		name        string
		wantErrMsg  string
		defaultRule bool
	}{
		{
			name: "default rule should be valid without query",
			rule: flagdef.Rule{
				Query:           nil,
				VariationResult: new(variationA),
			},
			defaultRule: true,
			variations:  variations,
			wantErr:     assert.NoError,
		},
		{
			name: "default rule should be valid with query",
			rule: flagdef.Rule{
				Query:           new(keyEqTest),
				VariationResult: new(variationA),
			},
			defaultRule: true,
			variations:  variations,
			wantErr:     assert.NoError,
		},
		{
			name: "non-default rule without query should fail",
			rule: flagdef.Rule{
				Query:           nil,
				VariationResult: new(variationA),
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.Error,
			wantErrMsg:  "each targeting should have a query",
		},
		{
			name: "non-default rule with empty query should fail",
			rule: flagdef.Rule{
				Query:           new(""),
				VariationResult: new(variationA),
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.Error,
		},
		{
			name: "non-default rule with valid Nikunjy query",
			rule: flagdef.Rule{
				Query:           new(keyEqTest),
				VariationResult: new(variationA),
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.NoError,
		},
		{
			name: "non-default rule with valid JSONLogic query",
			rule: flagdef.Rule{
				Query:           new(`{"==": [{"var": "key"}, "test"]}`),
				VariationResult: new(variationA),
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.NoError,
		},
		{
			name: "non-default rule with invalid JSONLogic query",
			rule: flagdef.Rule{
				Query:           new(`{"unknownOperator": [1, 2]}`),
				VariationResult: new(variationA),
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.Error,
			wantErrMsg:  "invalid jsonlogic query",
		},
		{
			name: "non-default rule with invalid Nikunjy query",
			rule: flagdef.Rule{
				Query:           new(invalidQuerySyntax),
				VariationResult: new(variationA),
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.Error,
			wantErrMsg:  invalidQuery,
		},
		{
			name: "non-default rule with complex valid Nikunjy query",
			rule: flagdef.Rule{
				Query:           new("key eq \"test\" and version gt 1.0.0"),
				VariationResult: new(variationA),
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.NoError,
		},
		{
			name: "non-default rule with valid Nikunjy query with OR",
			rule: flagdef.Rule{
				Query:           new("key eq \"test1\" or key eq \"test2\""),
				VariationResult: new(variationA),
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.NoError,
		},
		{
			name: "non-default rule with query containing whitespace",
			rule: flagdef.Rule{
				Query:           new("  key eq \"test\"  "),
				VariationResult: new(variationA),
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.NoError,
		},
		{
			name: "non-default rule with query containing newlines",
			rule: flagdef.Rule{
				Query:           new("key eq \"test\"\n"),
				VariationResult: new(variationA),
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.NoError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.rule.IsValid(flagdef.NewService(), tt.defaultRule, tt.variations)
			tt.wantErr(t, err, isValidErrorV, err)

			if tt.wantErrMsg != "" && err != nil {
				assert.Contains(t, err.Error(), tt.wantErrMsg)
			}
		})
	}
}

func TestRule_validateNikunjyQuery(t *testing.T) {
	t.Parallel()

	variations := map[string]*any{
		variationA: new(any(valueAUnderscore)),
	}

	tests := []struct {
		variations  map[string]*any
		wantErr     assert.ErrorAssertionFunc
		rule        flagdef.Rule
		name        string
		wantErrMsg  string
		defaultRule bool
	}{
		{
			name: "valid simple query",
			rule: flagdef.Rule{
				Query:           new(keyEqTest),
				VariationResult: new(variationA),
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.NoError,
		},
		{
			name: "valid query with AND",
			rule: flagdef.Rule{
				Query:           new("key eq \"test\" and version gt 1.0.0"),
				VariationResult: new(variationA),
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.NoError,
		},
		{
			name: "valid query with OR",
			rule: flagdef.Rule{
				Query:           new("key eq \"test1\" or key eq \"test2\""),
				VariationResult: new(variationA),
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.NoError,
		},
		{
			name: "valid query with comparison operators",
			rule: flagdef.Rule{
				Query:           new("age gt 18"),
				VariationResult: new(variationA),
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.NoError,
		},
		{
			name: "valid query with lt operator",
			rule: flagdef.Rule{
				Query:           new("age lt 65"),
				VariationResult: new(variationA),
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.NoError,
		},
		{
			name: "valid query with ge operator",
			rule: flagdef.Rule{
				Query:           new("score ge 100"),
				VariationResult: new(variationA),
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.NoError,
		},
		{
			name: "valid query with le operator",
			rule: flagdef.Rule{
				Query:           new("score le 200"),
				VariationResult: new(variationA),
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.NoError,
		},
		{
			name: "valid query with ne operator",
			rule: flagdef.Rule{
				Query:           new("status ne \"inactive\""),
				VariationResult: new(variationA),
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.NoError,
		},
		{
			name: "valid query with in operator",
			rule: flagdef.Rule{
				Query:           new("status in [\"active\", \"pending\"]"),
				VariationResult: new(variationA),
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.NoError,
		},
		{
			name: "valid query with parentheses",
			rule: flagdef.Rule{
				Query:           new("(key eq \"test1\" or key eq \"test2\") and version gt 1.0.0"),
				VariationResult: new(variationA),
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.NoError,
		},
		{
			name: invalidQuerySyntax,
			rule: flagdef.Rule{
				Query:           new(invalidQuerySyntax),
				VariationResult: new(variationA),
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.Error,
			wantErrMsg:  invalidQuery,
		},
		{
			name: "invalid query with missing operator",
			rule: flagdef.Rule{
				Query:           new("key \"test\""),
				VariationResult: new(variationA),
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.Error,
			wantErrMsg:  invalidQuery,
		},
		{
			name: "invalid query with unclosed quotes",
			rule: flagdef.Rule{
				Query:           new("key eq \"test"),
				VariationResult: new(variationA),
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.Error,
			wantErrMsg:  invalidQuery,
		},
		{
			name: "invalid query with malformed parentheses",
			rule: flagdef.Rule{
				Query:           new("key eq \"test\" and (version gt 1.0.0"),
				VariationResult: new(variationA),
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.Error,
			wantErrMsg:  invalidQuery,
		},
		{
			name: "empty query",
			rule: flagdef.Rule{
				Query:           new(""),
				VariationResult: new(variationA),
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.Error,
		},
		{
			name: "query with only whitespace",
			rule: flagdef.Rule{
				Query:           new("   "),
				VariationResult: new(variationA),
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.Error,
			wantErrMsg:  invalidQuery,
		},
		{
			name: "valid query with semver comparison",
			rule: flagdef.Rule{
				Query:           new("version gt 1.0.0"),
				VariationResult: new(variationA),
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.NoError,
		},
		{
			name: "valid query with date comparison",
			rule: flagdef.Rule{
				Query:           new("createdAt gt \"2023-01-01T00:00:00Z\""),
				VariationResult: new(variationA),
			},
			defaultRule: false,
			variations:  variations,
			wantErr:     assert.NoError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.rule.IsValid(flagdef.NewService(), tt.defaultRule, tt.variations)
			tt.wantErr(t, err, isValidErrorV, err)

			if tt.wantErrMsg != "" && err != nil {
				assert.Contains(t, err.Error(), tt.wantErrMsg)
			}
		})
	}
}

package flag_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/flag"
	"github.com/blackbirdworks/semaphore/modules/core/internalerror"
)

// ExpectRuleNotApplyError asserts that the error is (or wraps) a *RuleNotApplyError.
func ExpectRuleNotApplyError(t assert.TestingT, err error, msgAndArgs ...any) bool {
	var target *internalerror.RuleNotApplyError

	return assert.ErrorAs(t, err, &target, msgAndArgs...)
}

func TestRuleEvaluate(t *testing.T) {
	t.Parallel()

	type args struct {
		user      ffcontext.Context
		isDefault bool
	}
	tests := []struct {
		wantErr assert.ErrorAssertionFunc
		rule    flag.Rule
		args    args
		name    string
		want    string
	}{
		{
			name: "No query, default variation result",
			rule: flag.Rule{
				Name:            new("rule1"),
				VariationResult: new("variation_A"),
			},
			args: args{
				user: ffcontext.NewEvaluationContext("abc"),
			},
			want:    "variation_A",
			wantErr: assert.NoError,
		},
		{
			name: "Ignore query if default variation result",
			rule: flag.Rule{
				Name:            new("rule1"),
				VariationResult: new("variation_A"),
				Query:           new("key eq \"def\""),
			},
			args: args{
				isDefault: true,
				user:      ffcontext.NewEvaluationContext("abc"),
			},
			want:    "variation_A",
			wantErr: assert.NoError,
		},
		{
			name: "User does not match the query",
			rule: flag.Rule{
				Name:            new("rule1"),
				VariationResult: new("variation_A"),
				Query:           new("key eq \"def\""),
			},
			args: args{
				isDefault: false,
				user:      ffcontext.NewEvaluationContext("abc"),
			},
			wantErr: assert.Error,
		},
		{
			name: "User does not match the query (jsonlogic)",
			rule: flag.Rule{
				Name:            new("rule1"),
				VariationResult: new("variation_A"),
				Query:           new(`{"==": [{"var": "key"}, "def"]}`),
			},
			args: args{
				isDefault: false,
				user:      ffcontext.NewEvaluationContext("abc"),
			},
			wantErr: assert.Error,
		},
		{
			name: "User match the query",
			rule: flag.Rule{
				Name:            new("rule1"),
				VariationResult: new("variation_A"),
				Query:           new("key eq \"abc\""),
			},
			args: args{
				isDefault: false,
				user:      ffcontext.NewEvaluationContext("abc"),
			},
			want:    "variation_A",
			wantErr: assert.NoError,
		},
		{
			name: "JSONLogic rule return string 'true'",
			rule: flag.Rule{
				Name:            new("rule1"),
				VariationResult: new("variation_A"),
				Query:           new("{\"cat\": [\"t\", \"r\", \"u\", \"e\"]}"),
			},
			args: args{
				isDefault: false,
				user:      ffcontext.NewEvaluationContext("abc"),
			},
			want:    "variation_A",
			wantErr: assert.NoError,
		},

		{
			name: "User match the query (jsonlogic)",
			rule: flag.Rule{
				Name:            new("rule1"),
				VariationResult: new("variation_A"),
				Query:           new(`{"==": [{"var": "key"}, "abc"]}`),
			},
			args: args{
				isDefault: false,
				user:      ffcontext.NewEvaluationContext("abc"),
			},
			want:    "variation_A",
			wantErr: assert.NoError,
		},
		{
			// GHSA-mw4w-7w23-cg45: a context value shaped like JSON Logic must not be executed.
			name: "JSONLogic does not execute logic coming from the context",
			rule: flag.Rule{
				Name:            new("rule1"),
				VariationResult: new("variation_A"),
				Query:           new(`{"some":[{"var":"orgs"},{"==":[{"var":"plan"},"enterprise"]}]}`),
			},
			args: args{
				isDefault: false,
				user: ffcontext.NewEvaluationContextBuilder("abc").
					AddCustom("orgs", []any{map[string]any{"id": 1}}).
					AddCustom("plan", map[string]any{"cat": []any{"enter", "prise"}}).
					Build(),
			},
			wantErr: assert.Error,
		},
		{
			name: "Invalid json for query",
			rule: flag.Rule{
				Name:            new("rule1"),
				VariationResult: new("variation_A"),
				Query:           new(`{"==": [{"var": "key"}, "abc"]`),
			},
			args: args{
				isDefault: false,
				user:      ffcontext.NewEvaluationContext("abc"),
			},
			wantErr: assert.Error,
		},
		{
			name: "Invalid jsonlogic query (valid JSON but invalid query format)",
			rule: flag.Rule{
				Name:            new("rule1"),
				VariationResult: new("variation_A"),
				Query:           new(`{"xxx": [{"var": "key"}, "abc"]}`),
			},
			args: args{
				isDefault: false,
				user:      ffcontext.NewEvaluationContext("abc"),
			},
			wantErr: assert.Error,
		},
		{
			name: "No match and no default variation",
			rule: flag.Rule{
				Name: new("rule1"),
			},
			args:    args{},
			wantErr: assert.Error,
		},
		{
			name: "Percentage ignore variation result",
			rule: flag.Rule{
				Name:            new("rule1"),
				VariationResult: new("variation_A"),
				Percentages: &map[string]float64{
					"variation_B": 0,
					"variation_C": 100,
				},
			},
			args: args{
				user: ffcontext.NewEvaluationContext("userkey"),
			},
			wantErr: assert.NoError,
			want:    "variation_C",
		},
		{
			name: "Percentage in 1st bucket",
			rule: flag.Rule{
				Name: new("rule1"),
				Percentages: &map[string]float64{
					"variation_C": 9,
					"variation_B": 91,
				},
			},
			args: args{
				user: ffcontext.NewEvaluationContext("userkey"),
			},
			wantErr: assert.NoError,
			want:    "variation_C",
		},
		{
			name: "Percentage more than 100%",
			rule: flag.Rule{
				Name: new("rule1"),
				Percentages: &map[string]float64{
					"variation_C": 91,
					"variation_B": 91,
				},
			},
			args: args{
				user: ffcontext.NewEvaluationContext("userkey"),
			},
			wantErr: assert.NoError,
			want:    "variation_B",
		},
		{
			name: "Percentage in 2nd bucket",
			rule: flag.Rule{
				Name: new("rule1"),
				Percentages: &map[string]float64{
					"variation_D": 70,
					"variation_C": 10,
					"variation_B": 20,
				},
			},
			args: args{
				user: ffcontext.NewEvaluationContext("randomUserID"),
			},
			wantErr: assert.NoError,
			want:    "variation_C",
		},
		{
			name: "Percentage in 3rd bucket",
			rule: flag.Rule{
				Name: new("rule1"),
				Percentages: &map[string]float64{
					"variation_D": 70,
					"variation_C": 10,
					"variation_B": 20,
				},
			},
			args: args{
				user: ffcontext.NewEvaluationContext("96ac59e6-7492-436b-b15a-ba1d797d2423"),
			},
			wantErr: assert.NoError,
			want:    "variation_B",
		},
		{
			name: "Percentage + user match query",
			rule: flag.Rule{
				Name: new("rule1"),
				Percentages: &map[string]float64{
					"variation_D": 70,
					"variation_C": 10,
					"variation_B": 20,
				},
				Query: new("key eq \"96ac59e6-7492-436b-b15a-ba1d797d2423\""),
			},
			args: args{
				user: ffcontext.NewEvaluationContext("96ac59e6-7492-436b-b15a-ba1d797d2423"),
			},
			wantErr: assert.NoError,
			want:    "variation_B",
		},
		{
			name: "Progressive rollout ignore percentage",
			rule: flag.Rule{
				Name: new("rule1"),
				Percentages: &map[string]float64{
					"variation_A": 100,
				},
				ProgressiveRollout: &flag.ProgressiveRollout{
					Initial: &flag.ProgressiveRolloutStep{
						Variation:  new("variation_B"),
						Percentage: new(float64(0)),
						// Far-future dates so evaluation always lands before the
						// ramp and deterministically serves the initial variation
						// (avoids a timing race on loaded CI runners).
						Date: new(fixedNow().Add(1 * time.Hour)),
					},
					End: &flag.ProgressiveRolloutStep{
						Variation:  new("variation_C"),
						Percentage: new(float64(100)),
						Date:       new(fixedNow().Add(2 * time.Hour)),
					},
				},
			},
			args: args{
				user: ffcontext.NewEvaluationContext("userkey"),
			},
			wantErr: assert.NoError,
			want:    "variation_B",
		},
		{
			name: "Progressive rollout before ramp",
			rule: flag.Rule{
				Name: new("rule1"),
				ProgressiveRollout: &flag.ProgressiveRollout{
					Initial: &flag.ProgressiveRolloutStep{
						Variation:  new("variation_B"),
						Percentage: new(float64(0)),
						Date:       new(fixedNow().Add(1 * time.Second)),
					},
					End: &flag.ProgressiveRolloutStep{
						Variation:  new("variation_C"),
						Percentage: new(float64(100)),
						Date:       new(fixedNow().Add(3 * time.Second)),
					},
				},
			},
			args: args{
				user: ffcontext.NewEvaluationContext("userkey"),
			},
			wantErr: assert.NoError,
			want:    "variation_B",
		},
		{
			name: "Progressive rollout after ramp",
			rule: flag.Rule{
				Name: new("rule1"),
				ProgressiveRollout: &flag.ProgressiveRollout{
					Initial: &flag.ProgressiveRolloutStep{
						Variation:  new("variation_B"),
						Percentage: new(float64(0)),
						Date:       new(fixedNow().Add(-6 * time.Second)),
					},
					End: &flag.ProgressiveRolloutStep{
						Variation:  new("variation_C"),
						Percentage: new(float64(100)),
						Date:       new(fixedNow().Add(-3 * time.Second)),
					},
				},
			},
			args: args{
				user: ffcontext.NewEvaluationContext("userkey"),
			},
			wantErr: assert.NoError,
			want:    "variation_C",
		},
		{
			name: "Progressive rollout in the ramp serve initial variation",
			rule: flag.Rule{
				Name: new("rule1"),
				ProgressiveRollout: &flag.ProgressiveRollout{
					Initial: &flag.ProgressiveRolloutStep{
						Variation:  new("variation_B"),
						Percentage: new(float64(0)),
						Date:       new(fixedNow().Add(-1 * time.Second)),
					},
					End: &flag.ProgressiveRolloutStep{
						Variation:  new("variation_C"),
						Percentage: new(float64(100)),
						Date:       new(fixedNow().Add(3 * time.Second)),
					},
				},
			},
			args: args{
				user: ffcontext.NewEvaluationContext("userKey"),
			},
			wantErr: assert.NoError,
			want:    "variation_B",
		},
		{
			name: "Progressive rollout in the ramp serve end variation",
			rule: flag.Rule{
				Name: new("rule1"),
				ProgressiveRollout: &flag.ProgressiveRollout{
					Initial: &flag.ProgressiveRolloutStep{
						Variation:  new("variation_B"),
						Percentage: new(float64(0)),
						Date:       new(fixedNow().Add(-3 * time.Second)),
					},
					End: &flag.ProgressiveRolloutStep{
						Variation:  new("variation_C"),
						Percentage: new(float64(100)),
						Date:       new(fixedNow().Add(1 * time.Second)),
					},
				},
			},
			args: args{
				user: ffcontext.NewEvaluationContext("userkey"),
			},
			wantErr: assert.NoError,
			want:    "variation_C",
		},
		{
			name: "Progressive rollout in the ramp serve end variation no percentage specified",
			rule: flag.Rule{
				Name: new("rule1"),
				ProgressiveRollout: &flag.ProgressiveRollout{
					Initial: &flag.ProgressiveRolloutStep{
						Variation: new("variation_B"),
						Date:      new(fixedNow().Add(-3 * time.Second)),
					},
					End: &flag.ProgressiveRolloutStep{
						Variation: new("variation_C"),
						Date:      new(fixedNow().Add(1 * time.Second)),
					},
				},
			},
			args: args{
				user: ffcontext.NewEvaluationContext("userkey"),
			},
			wantErr: assert.NoError,
			want:    "variation_C",
		},
		{
			name: "Progressive rollout initial before end",
			rule: flag.Rule{
				Name: new("rule1"),
				ProgressiveRollout: &flag.ProgressiveRollout{
					Initial: &flag.ProgressiveRolloutStep{
						Variation:  new("variation_B"),
						Percentage: new(float64(0)),
						Date:       new(fixedNow().Add(3 * time.Second)),
					},
					End: &flag.ProgressiveRolloutStep{
						Variation:  new("variation_C"),
						Percentage: new(float64(100)),
						Date:       new(fixedNow().Add(1 * time.Second)),
					},
				},
			},
			args:    args{},
			wantErr: assert.Error,
		},
		{
			name: "Progressive rollout no initial step",
			rule: flag.Rule{
				Name: new("rule1"),
				ProgressiveRollout: &flag.ProgressiveRollout{
					End: &flag.ProgressiveRolloutStep{
						Variation:  new("variation_C"),
						Percentage: new(float64(100)),
						Date:       new(fixedNow().Add(1 * time.Second)),
					},
				},
			},
			args:    args{},
			wantErr: assert.Error,
		},
		{
			name: "Progressive rollout no end step",
			rule: flag.Rule{
				Name: new("rule1"),
				ProgressiveRollout: &flag.ProgressiveRollout{
					Initial: &flag.ProgressiveRolloutStep{
						Variation:  new("variation_B"),
						Percentage: new(float64(0)),
						Date:       new(fixedNow().Add(3 * time.Second)),
					},
				},
			},
			args:    args{},
			wantErr: assert.Error,
		},
		{
			name: "Progressive rollout initial step with no variation",
			rule: flag.Rule{
				Name: new("rule1"),
				ProgressiveRollout: &flag.ProgressiveRollout{
					Initial: &flag.ProgressiveRolloutStep{
						Percentage: new(float64(0)),
						Date:       new(fixedNow().Add(3 * time.Second)),
					},
					End: &flag.ProgressiveRolloutStep{
						Variation:  new("variation_C"),
						Percentage: new(float64(100)),
						Date:       new(fixedNow().Add(1 * time.Second)),
					},
				},
			},
			args:    args{},
			wantErr: assert.Error,
		},
		{
			name: "Progressive rollout end step with no variation",
			rule: flag.Rule{
				Name: new("rule1"),
				ProgressiveRollout: &flag.ProgressiveRollout{
					Initial: &flag.ProgressiveRolloutStep{
						Variation:  new("variation_B"),
						Percentage: new(float64(0)),
						Date:       new(fixedNow().Add(3 * time.Second)),
					},
					End: &flag.ProgressiveRolloutStep{
						Percentage: new(float64(100)),
						Date:       new(fixedNow().Add(1 * time.Second)),
					},
				},
			},
			args:    args{},
			wantErr: assert.Error,
		},
		{
			name: "Progressive rollout no initial date",
			rule: flag.Rule{
				Name: new("rule1"),
				ProgressiveRollout: &flag.ProgressiveRollout{
					Initial: &flag.ProgressiveRolloutStep{
						Variation:  new("variation_B"),
						Percentage: new(float64(0)),
					},
					End: &flag.ProgressiveRolloutStep{
						Variation:  new("variation_C"),
						Percentage: new(float64(100)),
						Date:       new(fixedNow().Add(1 * time.Second)),
					},
				},
			},
			args:    args{},
			wantErr: assert.Error,
		},
		{
			name: "Progressive rollout no end date",
			rule: flag.Rule{
				Name: new("rule1"),
				ProgressiveRollout: &flag.ProgressiveRollout{
					Initial: &flag.ProgressiveRolloutStep{
						Variation:  new("variation_B"),
						Percentage: new(float64(0)),
						Date:       new(fixedNow().Add(1 * time.Second)),
					},
					End: &flag.ProgressiveRolloutStep{
						Variation:  new("variation_C"),
						Percentage: new(float64(100)),
					},
				},
			},
			args:    args{},
			wantErr: assert.Error,
		},
		{
			name: "User does not match the query (JsonLogic)",
			rule: flag.Rule{
				Name:            new("rule1"),
				VariationResult: new("variation_A"),
				Query:           new(`{"==": [{"var": "key"}, "def"]}`),
			},
			args: args{
				isDefault: false,
				user:      ffcontext.NewEvaluationContext("abc"),
			},
			wantErr: assert.Error,
		},
		{
			name: "User partially matches with in array operator (JsonLogic)",
			rule: flag.Rule{
				Name:            new("rule1"),
				VariationResult: new("variation_A"),
				Query:           new(`{"in": [{"var": "key"}, ["abcdef", "1234"]]}`),
			},
			args: args{
				isDefault: false,
				user:      ffcontext.NewEvaluationContext("abc"),
			},
			wantErr: assert.Error,
		},
		{
			name: "User match the query (JsonLogic)",
			rule: flag.Rule{
				Name:            new("rule1"),
				VariationResult: new("variation_A"),
				Query:           new(`{"==": [{"var": "key"}, "abc"]}`),
			},
			args: args{
				isDefault: false,
				user:      ffcontext.NewEvaluationContext("abc"),
			},
			want:    "variation_A",
			wantErr: assert.NoError,
		},
		{
			name: "Percentage + user match query (JsonLogic)",
			rule: flag.Rule{
				Name: new("rule1"),
				Percentages: &map[string]float64{
					"variation_D": 70,
					"variation_C": 10,
					"variation_B": 20,
				},
				Query: new(
					`{"==": [{"var": "key"}, "96ac59e6-7492-436b-b15a-ba1d797d2423"]}`,
				),
			},
			args: args{
				user: ffcontext.NewEvaluationContext("96ac59e6-7492-436b-b15a-ba1d797d2423"),
			},
			wantErr: assert.NoError,
			want:    "variation_B",
		},
		{
			name: "Invalid JsonLogic logic rule",
			rule: flag.Rule{
				Name: new("rule1"),
				Percentages: &map[string]float64{
					"variation_D": 70,
					"variation_C": 10,
					"variation_B": 20,
				},
				Query: new(
					`{"=": [{"var": "key"}, "96ac59e6-7492-436b-b15a-ba1d797d2423"]}`,
				),
			},
			args: args{
				user: ffcontext.NewEvaluationContext("96ac59e6-7492-436b-b15a-ba1d797d2423"),
			},
			wantErr: assert.Error,
		},
		{
			name: "Semver comparison with prerelease identifiers - higher version provided",
			rule: flag.Rule{
				Name:            new("semver_rule"),
				VariationResult: new("variation_A"),
				Query:           new("version gt 1.0.0-10"),
			},
			args: args{
				isDefault: false,
				user: ffcontext.NewEvaluationContextBuilder("user-key").
					AddCustom("version", "1.0.0-11").
					Build(),
			},
			want:    "variation_A",
			wantErr: assert.NoError,
		},
		{
			name: "Semver comparison with prerelease identifiers - lower version provided",
			rule: flag.Rule{
				Name:            new("semver_rule"),
				VariationResult: new("variation_A"),
				Query:           new("version gt 1.0.0-10"),
			},
			args: args{
				isDefault: false,
				user: ffcontext.NewEvaluationContextBuilder("user-key").
					AddCustom("version", "1.0.0-2").
					Build(),
			},
			want:    "",
			wantErr: ExpectRuleNotApplyError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			key := ""
			if tt.args.user != nil {
				key = tt.args.user.GetKey()
			}
			got, err := tt.rule.Evaluate(
				flag.NewService(),
				key,
				atFixedNow(tt.args.user),
				"flagname+",
				tt.args.isDefault,
			)

			if !tt.wantErr(
				t,
				err,
				fmt.Sprintf("Evaluate(%v, %v)", tt.args.user, tt.args.isDefault),
			) {
				return
			}
			assert.Equalf(t, tt.want, got, "Evaluate(%v, %v)", tt.args.user, tt.args.isDefault)
		})
	}
}

func TestRule_MergeRules(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		originalRule flag.Rule
		updatedRule  flag.Rule
		want         flag.Rule
	}{
		{
			name: "merge simple rule",
			originalRule: flag.Rule{
				Name:            new("rule1"),
				VariationResult: new("variation_A"),
			},
			updatedRule: flag.Rule{
				VariationResult: new("variation_B"),
			},
			want: flag.Rule{
				Name:            new("rule1"),
				VariationResult: new("variation_B"),
			},
		},
		{
			name: "merge rule with disable set to true",
			originalRule: flag.Rule{
				Name:            new("rule1"),
				VariationResult: new("variation_A"),
			},
			updatedRule: flag.Rule{
				Disable: new(true),
			},
			want: flag.Rule{
				Name:            new("rule1"),
				VariationResult: new("variation_A"),
				Disable:         new(true),
			},
		},
		{
			name: "merge rule with disable set to false",
			originalRule: flag.Rule{
				Name:            new("rule1"),
				VariationResult: new("variation_A"),
				Disable:         new(true),
			},
			updatedRule: flag.Rule{
				Disable: new(false),
			},
			want: flag.Rule{
				Name:            new("rule1"),
				VariationResult: new("variation_A"),
				Disable:         new(false),
			},
		},
		{
			name: "merge percentage",
			originalRule: flag.Rule{
				Name: new("rule1"),
				Percentages: &map[string]float64{
					"variation_A": 10,
					"variation_B": 10,
					"variation_C": 10,
					"variation_D": 10,
					"variation_E": 60,
				},
			},
			updatedRule: flag.Rule{
				Percentages: &map[string]float64{
					"variation_D": -1,
					"variation_E": 50,
					"variation_F": 20,
				},
			},
			want: flag.Rule{
				Name: new("rule1"),
				Percentages: &map[string]float64{
					"variation_A": 10,
					"variation_B": 10,
					"variation_C": 10,
					"variation_E": 50,
					"variation_F": 20,
				},
			},
		},
		{
			name: "merge rule with query",
			originalRule: flag.Rule{
				Name:            new("rule1"),
				VariationResult: new("variation_A"),
				Query:           new("key eq \"abc\""),
			},
			updatedRule: flag.Rule{
				Query: new("key eq \"cde\""),
			},
			want: flag.Rule{
				Name:            new("rule1"),
				VariationResult: new("variation_A"),
				Query:           new("key eq \"cde\""),
			},
		},
		{
			name: "merge rule remove query",
			originalRule: flag.Rule{
				Name:            new("rule1"),
				VariationResult: new("variation_A"),
				Query:           new("key eq \"abc\""),
			},
			updatedRule: flag.Rule{
				Query: new(""),
			},
			want: flag.Rule{
				Name:            new("rule1"),
				VariationResult: new("variation_A"),
				Query:           new(""),
			},
		},
		{
			name: "merge rule with progressive rollout no rollout before",
			originalRule: flag.Rule{
				Name:            new("rule1"),
				VariationResult: new("variation_A"),
			},
			updatedRule: flag.Rule{
				ProgressiveRollout: &flag.ProgressiveRollout{
					Initial: &flag.ProgressiveRolloutStep{
						Variation: new("variation_B"),
						Date: new(
							time.Date(2021, time.February, 1, 10, 10, 10, 10, time.UTC),
						),
					},
					End: &flag.ProgressiveRolloutStep{
						Variation: new("variation_C"),
						Date: new(
							time.Date(2021, time.February, 3, 10, 10, 10, 10, time.UTC),
						),
					},
				},
			},
			want: flag.Rule{
				Name:            new("rule1"),
				VariationResult: new("variation_A"),
				ProgressiveRollout: &flag.ProgressiveRollout{
					Initial: &flag.ProgressiveRolloutStep{
						Variation: new("variation_B"),
						Date: new(
							time.Date(2021, time.February, 1, 10, 10, 10, 10, time.UTC),
						),
					},
					End: &flag.ProgressiveRolloutStep{
						Variation: new("variation_C"),
						Date: new(
							time.Date(2021, time.February, 3, 10, 10, 10, 10, time.UTC),
						),
					},
				},
			},
		},
		{
			name: "merge rule with progressive rollout update initial step",
			originalRule: flag.Rule{
				Name:            new("rule1"),
				VariationResult: new("variation_A"),
				ProgressiveRollout: &flag.ProgressiveRollout{
					Initial: &flag.ProgressiveRolloutStep{
						Variation: new("variation_B"),
						Date: new(
							time.Date(2021, time.February, 1, 10, 10, 10, 10, time.UTC),
						),
					},
					End: &flag.ProgressiveRolloutStep{
						Variation: new("variation_C"),
						Date: new(
							time.Date(2021, time.February, 3, 10, 10, 10, 10, time.UTC),
						),
					},
				},
			},
			updatedRule: flag.Rule{
				ProgressiveRollout: &flag.ProgressiveRollout{
					Initial: &flag.ProgressiveRolloutStep{
						Variation:  new("variation_D"),
						Percentage: new(float64(40)),
						Date: new(
							time.Date(2021, time.February, 1, 10, 10, 10, 10, time.UTC),
						),
					},
				},
			},
			want: flag.Rule{
				Name:            new("rule1"),
				VariationResult: new("variation_A"),
				ProgressiveRollout: &flag.ProgressiveRollout{
					Initial: &flag.ProgressiveRolloutStep{
						Variation:  new("variation_D"),
						Percentage: new(float64(40)),
						Date: new(
							time.Date(2021, time.February, 1, 10, 10, 10, 10, time.UTC),
						),
					},
					End: &flag.ProgressiveRolloutStep{
						Variation: new("variation_C"),
						Date: new(
							time.Date(2021, time.February, 3, 10, 10, 10, 10, time.UTC),
						),
					},
				},
			},
		},
		{
			name: "merge rule with progressive rollout update end step",
			originalRule: flag.Rule{
				Name:            new("rule1"),
				VariationResult: new("variation_A"),
				ProgressiveRollout: &flag.ProgressiveRollout{
					Initial: &flag.ProgressiveRolloutStep{
						Variation: new("variation_B"),
						Date: new(
							time.Date(2021, time.February, 1, 10, 10, 10, 10, time.UTC),
						),
					},
					End: &flag.ProgressiveRolloutStep{
						Variation: new("variation_C"),
						Date: new(
							time.Date(2021, time.February, 3, 10, 10, 10, 10, time.UTC),
						),
					},
				},
			},
			updatedRule: flag.Rule{
				ProgressiveRollout: &flag.ProgressiveRollout{
					End: &flag.ProgressiveRolloutStep{
						Date: new(
							time.Date(2021, time.February, 12, 10, 10, 10, 10, time.UTC),
						),
						Percentage: new(float64(100)),
					},
				},
			},
			want: flag.Rule{
				Name:            new("rule1"),
				VariationResult: new("variation_A"),
				ProgressiveRollout: &flag.ProgressiveRollout{
					Initial: &flag.ProgressiveRolloutStep{
						Variation: new("variation_B"),
						Date: new(
							time.Date(2021, time.February, 1, 10, 10, 10, 10, time.UTC),
						),
					},
					End: &flag.ProgressiveRolloutStep{
						Variation: new("variation_C"),
						Date: new(
							time.Date(2021, time.February, 12, 10, 10, 10, 10, time.UTC),
						),
						Percentage: new(float64(100)),
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tt.originalRule.MergeRules(tt.updatedRule)
			assert.Equal(t, tt.want, tt.originalRule)
		})
	}
}

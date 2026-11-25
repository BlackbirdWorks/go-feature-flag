package flag_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/flag"
	"github.com/blackbirdworks/semaphore/modules/core/internalerror"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

const (
	userKeyUUID = "96ac59e6-7492-436b-b15a-ba1d797d2423"
	keyEqAbc    = "key eq \"abc\""
	userkey     = "userkey"
	variationE  = "variation_E"
)

type ruleEvaluateArgs struct {
	user      ffcontext.Context
	isDefault bool
}

type ruleEvaluateCase struct {
	wantErr assert.ErrorAssertionFunc
	rule    flag.Rule
	args    ruleEvaluateArgs
	name    string
	want    string
}

// ExpectRuleNotApplyError asserts that the error is (or wraps) a *RuleNotApplyError.
func ExpectRuleNotApplyError(t assert.TestingT, err error, msgAndArgs ...any) bool {
	var target *internalerror.RuleNotApplyError

	return assert.ErrorAs(t, err, &target, msgAndArgs...)
}

func runRuleEvaluateTests(t *testing.T, tests []ruleEvaluateCase) {
	t.Helper()

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

func TestRuleEvaluate(t *testing.T) {
	t.Parallel()

	runRuleEvaluateTests(t, []ruleEvaluateCase{
		{
			name: "No query, default variation result",
			rule: flag.Rule{
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationA),
			},
			args: ruleEvaluateArgs{
				user: ffcontext.NewEvaluationContext(fixtures.Abc),
			},
			want:    variationA,
			wantErr: assert.NoError,
		},
		{
			name: "Ignore query if default variation result",
			rule: flag.Rule{
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationA),
				Query:           new("key eq \"def\""),
			},
			args: ruleEvaluateArgs{
				isDefault: true,
				user:      ffcontext.NewEvaluationContext(fixtures.Abc),
			},
			want:    variationA,
			wantErr: assert.NoError,
		},
		{
			name: "User does not match the query",
			rule: flag.Rule{
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationA),
				Query:           new("key eq \"def\""),
			},
			args: ruleEvaluateArgs{
				isDefault: false,
				user:      ffcontext.NewEvaluationContext(fixtures.Abc),
			},
			wantErr: assert.Error,
		},
		{
			name: "User does not match the query (jsonlogic)",
			rule: flag.Rule{
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationA),
				Query:           new(`{"==": [{"var": "key"}, "def"]}`),
			},
			args: ruleEvaluateArgs{
				isDefault: false,
				user:      ffcontext.NewEvaluationContext(fixtures.Abc),
			},
			wantErr: assert.Error,
		},
		{
			name: "User match the query",
			rule: flag.Rule{
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationA),
				Query:           new(keyEqAbc),
			},
			args: ruleEvaluateArgs{
				isDefault: false,
				user:      ffcontext.NewEvaluationContext(fixtures.Abc),
			},
			want:    variationA,
			wantErr: assert.NoError,
		},
		{
			name: "JSONLogic rule return string 'true'",
			rule: flag.Rule{
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationA),
				Query:           new("{\"cat\": [\"t\", \"r\", \"u\", \"e\"]}"),
			},
			args: ruleEvaluateArgs{
				isDefault: false,
				user:      ffcontext.NewEvaluationContext(fixtures.Abc),
			},
			want:    variationA,
			wantErr: assert.NoError,
		},

		{
			name: "User match the query (jsonlogic)",
			rule: flag.Rule{
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationA),
				Query:           new(`{"==": [{"var": "key"}, "abc"]}`),
			},
			args: ruleEvaluateArgs{
				isDefault: false,
				user:      ffcontext.NewEvaluationContext(fixtures.Abc),
			},
			want:    variationA,
			wantErr: assert.NoError,
		},
		{
			// GHSA-mw4w-7w23-cg45: a context value shaped like JSON Logic must not be executed.
			name: "JSONLogic does not execute logic coming from the context",
			rule: flag.Rule{
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationA),
				Query:           new(`{"some":[{"var":"orgs"},{"==":[{"var":"plan"},"enterprise"]}]}`),
			},
			args: ruleEvaluateArgs{
				isDefault: false,
				user: ffcontext.NewEvaluationContextBuilder(fixtures.Abc).
					AddCustom("orgs", []any{map[string]any{"id": 1}}).
					AddCustom(fixtures.Plan, map[string]any{"cat": []any{"enter", "prise"}}).
					Build(),
			},
			wantErr: assert.Error,
		},
		{
			name: "Invalid json for query",
			rule: flag.Rule{
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationA),
				Query:           new(`{"==": [{"var": "key"}, "abc"]`),
			},
			args: ruleEvaluateArgs{
				isDefault: false,
				user:      ffcontext.NewEvaluationContext(fixtures.Abc),
			},
			wantErr: assert.Error,
		},
		{
			name: "Invalid jsonlogic query (valid JSON but invalid query format)",
			rule: flag.Rule{
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationA),
				Query:           new(`{"xxx": [{"var": "key"}, "abc"]}`),
			},
			args: ruleEvaluateArgs{
				isDefault: false,
				user:      ffcontext.NewEvaluationContext(fixtures.Abc),
			},
			wantErr: assert.Error,
		},
		{
			name: "No match and no default variation",
			rule: flag.Rule{
				Name: new(fixtures.RuleOne),
			},
			args:    ruleEvaluateArgs{},
			wantErr: assert.Error,
		},
		{
			name: "Percentage ignore variation result",
			rule: flag.Rule{
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationA),
				Percentages: &map[string]float64{
					variationB: 0,
					variationC: 100,
				},
			},
			args: ruleEvaluateArgs{
				user: ffcontext.NewEvaluationContext(userkey),
			},
			wantErr: assert.NoError,
			want:    variationC,
		},
		{
			name: "Percentage in 1st bucket",
			rule: flag.Rule{
				Name: new(fixtures.RuleOne),
				Percentages: &map[string]float64{
					variationC: 9,
					variationB: 91,
				},
			},
			args: ruleEvaluateArgs{
				user: ffcontext.NewEvaluationContext(userkey),
			},
			wantErr: assert.NoError,
			want:    variationC,
		},
		{
			name: "Percentage more than 100%",
			rule: flag.Rule{
				Name: new(fixtures.RuleOne),
				Percentages: &map[string]float64{
					variationC: 91,
					variationB: 91,
				},
			},
			args: ruleEvaluateArgs{
				user: ffcontext.NewEvaluationContext(userkey),
			},
			wantErr: assert.NoError,
			want:    variationB,
		},
		{
			name: "Percentage in 2nd bucket",
			rule: flag.Rule{
				Name: new(fixtures.RuleOne),
				Percentages: &map[string]float64{
					variationD: 70,
					variationC: 10,
					variationB: 20,
				},
			},
			args: ruleEvaluateArgs{
				user: ffcontext.NewEvaluationContext("randomUserID"),
			},
			wantErr: assert.NoError,
			want:    variationC,
		},
		{
			name: "Percentage in 3rd bucket",
			rule: flag.Rule{
				Name: new(fixtures.RuleOne),
				Percentages: &map[string]float64{
					variationD: 70,
					variationC: 10,
					variationB: 20,
				},
			},
			args: ruleEvaluateArgs{
				user: ffcontext.NewEvaluationContext(userKeyUUID),
			},
			wantErr: assert.NoError,
			want:    variationB,
		},
		{
			name: "Percentage + user match query",
			rule: flag.Rule{
				Name: new(fixtures.RuleOne),
				Percentages: &map[string]float64{
					variationD: 70,
					variationC: 10,
					variationB: 20,
				},
				Query: new("key eq \"96ac59e6-7492-436b-b15a-ba1d797d2423\""),
			},
			args: ruleEvaluateArgs{
				user: ffcontext.NewEvaluationContext(userKeyUUID),
			},
			wantErr: assert.NoError,
			want:    variationB,
		},
		{
			name: "Progressive rollout ignore percentage",
			rule: flag.Rule{
				Name: new(fixtures.RuleOne),
				Percentages: &map[string]float64{
					variationA: 100,
				},
				ProgressiveRollout: &flag.ProgressiveRollout{
					Initial: &flag.ProgressiveRolloutStep{
						Variation:  new(variationB),
						Percentage: new(float64(0)),
						// Far-future dates so evaluation always lands before the
						// ramp and deterministically serves the initial variation
						// (avoids a timing race on loaded CI runners).
						Date: new(fixedNow().Add(1 * time.Hour)),
					},
					End: &flag.ProgressiveRolloutStep{
						Variation:  new(variationC),
						Percentage: new(float64(100)),
						Date:       new(fixedNow().Add(2 * time.Hour)),
					},
				},
			},
			args: ruleEvaluateArgs{
				user: ffcontext.NewEvaluationContext(userkey),
			},
			wantErr: assert.NoError,
			want:    variationB,
		},
	})
}

func TestRuleEvaluate_ProgressiveRollout(t *testing.T) {
	t.Parallel()

	runRuleEvaluateTests(t, []ruleEvaluateCase{
		{
			name: "Progressive rollout before ramp",
			rule: flag.Rule{
				Name: new(fixtures.RuleOne),
				ProgressiveRollout: &flag.ProgressiveRollout{
					Initial: &flag.ProgressiveRolloutStep{
						Variation:  new(variationB),
						Percentage: new(float64(0)),
						Date:       new(fixedNow().Add(1 * time.Second)),
					},
					End: &flag.ProgressiveRolloutStep{
						Variation:  new(variationC),
						Percentage: new(float64(100)),
						Date:       new(fixedNow().Add(3 * time.Second)),
					},
				},
			},
			args: ruleEvaluateArgs{
				user: ffcontext.NewEvaluationContext(userkey),
			},
			wantErr: assert.NoError,
			want:    variationB,
		},
		{
			name: "Progressive rollout after ramp",
			rule: flag.Rule{
				Name: new(fixtures.RuleOne),
				ProgressiveRollout: &flag.ProgressiveRollout{
					Initial: &flag.ProgressiveRolloutStep{
						Variation:  new(variationB),
						Percentage: new(float64(0)),
						Date:       new(fixedNow().Add(-6 * time.Second)),
					},
					End: &flag.ProgressiveRolloutStep{
						Variation:  new(variationC),
						Percentage: new(float64(100)),
						Date:       new(fixedNow().Add(-3 * time.Second)),
					},
				},
			},
			args: ruleEvaluateArgs{
				user: ffcontext.NewEvaluationContext(userkey),
			},
			wantErr: assert.NoError,
			want:    variationC,
		},
		{
			name: "Progressive rollout in the ramp serve initial variation",
			rule: flag.Rule{
				Name: new(fixtures.RuleOne),
				ProgressiveRollout: &flag.ProgressiveRollout{
					Initial: &flag.ProgressiveRolloutStep{
						Variation:  new(variationB),
						Percentage: new(float64(0)),
						Date:       new(fixedNow().Add(-1 * time.Second)),
					},
					End: &flag.ProgressiveRolloutStep{
						Variation:  new(variationC),
						Percentage: new(float64(100)),
						Date:       new(fixedNow().Add(3 * time.Second)),
					},
				},
			},
			args: ruleEvaluateArgs{
				user: ffcontext.NewEvaluationContext("userKey"),
			},
			wantErr: assert.NoError,
			want:    variationB,
		},
		{
			name: "Progressive rollout in the ramp serve end variation",
			rule: flag.Rule{
				Name: new(fixtures.RuleOne),
				ProgressiveRollout: &flag.ProgressiveRollout{
					Initial: &flag.ProgressiveRolloutStep{
						Variation:  new(variationB),
						Percentage: new(float64(0)),
						Date:       new(fixedNow().Add(-3 * time.Second)),
					},
					End: &flag.ProgressiveRolloutStep{
						Variation:  new(variationC),
						Percentage: new(float64(100)),
						Date:       new(fixedNow().Add(1 * time.Second)),
					},
				},
			},
			args: ruleEvaluateArgs{
				user: ffcontext.NewEvaluationContext(userkey),
			},
			wantErr: assert.NoError,
			want:    variationC,
		},
		{
			name: "Progressive rollout in the ramp serve end variation no percentage specified",
			rule: flag.Rule{
				Name: new(fixtures.RuleOne),
				ProgressiveRollout: &flag.ProgressiveRollout{
					Initial: &flag.ProgressiveRolloutStep{
						Variation: new(variationB),
						Date:      new(fixedNow().Add(-3 * time.Second)),
					},
					End: &flag.ProgressiveRolloutStep{
						Variation: new(variationC),
						Date:      new(fixedNow().Add(1 * time.Second)),
					},
				},
			},
			args: ruleEvaluateArgs{
				user: ffcontext.NewEvaluationContext(userkey),
			},
			wantErr: assert.NoError,
			want:    variationC,
		},
	})
}

func TestRuleEvaluate_ProgressiveRolloutErrors(t *testing.T) {
	t.Parallel()

	runRuleEvaluateTests(t, []ruleEvaluateCase{
		{
			name: "Progressive rollout initial before end",
			rule: flag.Rule{
				Name: new(fixtures.RuleOne),
				ProgressiveRollout: &flag.ProgressiveRollout{
					Initial: &flag.ProgressiveRolloutStep{
						Variation:  new(variationB),
						Percentage: new(float64(0)),
						Date:       new(fixedNow().Add(3 * time.Second)),
					},
					End: &flag.ProgressiveRolloutStep{
						Variation:  new(variationC),
						Percentage: new(float64(100)),
						Date:       new(fixedNow().Add(1 * time.Second)),
					},
				},
			},
			args:    ruleEvaluateArgs{},
			wantErr: assert.Error,
		},
		{
			name: "Progressive rollout no initial step",
			rule: flag.Rule{
				Name: new(fixtures.RuleOne),
				ProgressiveRollout: &flag.ProgressiveRollout{
					End: &flag.ProgressiveRolloutStep{
						Variation:  new(variationC),
						Percentage: new(float64(100)),
						Date:       new(fixedNow().Add(1 * time.Second)),
					},
				},
			},
			args:    ruleEvaluateArgs{},
			wantErr: assert.Error,
		},
		{
			name: "Progressive rollout no end step",
			rule: flag.Rule{
				Name: new(fixtures.RuleOne),
				ProgressiveRollout: &flag.ProgressiveRollout{
					Initial: &flag.ProgressiveRolloutStep{
						Variation:  new(variationB),
						Percentage: new(float64(0)),
						Date:       new(fixedNow().Add(3 * time.Second)),
					},
				},
			},
			args:    ruleEvaluateArgs{},
			wantErr: assert.Error,
		},
		{
			name: "Progressive rollout initial step with no variation",
			rule: flag.Rule{
				Name: new(fixtures.RuleOne),
				ProgressiveRollout: &flag.ProgressiveRollout{
					Initial: &flag.ProgressiveRolloutStep{
						Percentage: new(float64(0)),
						Date:       new(fixedNow().Add(3 * time.Second)),
					},
					End: &flag.ProgressiveRolloutStep{
						Variation:  new(variationC),
						Percentage: new(float64(100)),
						Date:       new(fixedNow().Add(1 * time.Second)),
					},
				},
			},
			args:    ruleEvaluateArgs{},
			wantErr: assert.Error,
		},
		{
			name: "Progressive rollout end step with no variation",
			rule: flag.Rule{
				Name: new(fixtures.RuleOne),
				ProgressiveRollout: &flag.ProgressiveRollout{
					Initial: &flag.ProgressiveRolloutStep{
						Variation:  new(variationB),
						Percentage: new(float64(0)),
						Date:       new(fixedNow().Add(3 * time.Second)),
					},
					End: &flag.ProgressiveRolloutStep{
						Percentage: new(float64(100)),
						Date:       new(fixedNow().Add(1 * time.Second)),
					},
				},
			},
			args:    ruleEvaluateArgs{},
			wantErr: assert.Error,
		},
		{
			name: "Progressive rollout no initial date",
			rule: flag.Rule{
				Name: new(fixtures.RuleOne),
				ProgressiveRollout: &flag.ProgressiveRollout{
					Initial: &flag.ProgressiveRolloutStep{
						Variation:  new(variationB),
						Percentage: new(float64(0)),
					},
					End: &flag.ProgressiveRolloutStep{
						Variation:  new(variationC),
						Percentage: new(float64(100)),
						Date:       new(fixedNow().Add(1 * time.Second)),
					},
				},
			},
			args:    ruleEvaluateArgs{},
			wantErr: assert.Error,
		},
		{
			name: "Progressive rollout no end date",
			rule: flag.Rule{
				Name: new(fixtures.RuleOne),
				ProgressiveRollout: &flag.ProgressiveRollout{
					Initial: &flag.ProgressiveRolloutStep{
						Variation:  new(variationB),
						Percentage: new(float64(0)),
						Date:       new(fixedNow().Add(1 * time.Second)),
					},
					End: &flag.ProgressiveRolloutStep{
						Variation:  new(variationC),
						Percentage: new(float64(100)),
					},
				},
			},
			args:    ruleEvaluateArgs{},
			wantErr: assert.Error,
		},
	})
}

func TestRuleEvaluate_JsonLogicAndSemver(t *testing.T) {
	t.Parallel()

	runRuleEvaluateTests(t, []ruleEvaluateCase{
		{
			name: "User does not match the query (JsonLogic)",
			rule: flag.Rule{
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationA),
				Query:           new(`{"==": [{"var": "key"}, "def"]}`),
			},
			args: ruleEvaluateArgs{
				isDefault: false,
				user:      ffcontext.NewEvaluationContext(fixtures.Abc),
			},
			wantErr: assert.Error,
		},
		{
			name: "User partially matches with in array operator (JsonLogic)",
			rule: flag.Rule{
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationA),
				Query:           new(`{"in": [{"var": "key"}, ["abcdef", "1234"]]}`),
			},
			args: ruleEvaluateArgs{
				isDefault: false,
				user:      ffcontext.NewEvaluationContext(fixtures.Abc),
			},
			wantErr: assert.Error,
		},
		{
			name: "User match the query (JsonLogic)",
			rule: flag.Rule{
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationA),
				Query:           new(`{"==": [{"var": "key"}, "abc"]}`),
			},
			args: ruleEvaluateArgs{
				isDefault: false,
				user:      ffcontext.NewEvaluationContext(fixtures.Abc),
			},
			want:    variationA,
			wantErr: assert.NoError,
		},
		{
			name: "Percentage + user match query (JsonLogic)",
			rule: flag.Rule{
				Name: new(fixtures.RuleOne),
				Percentages: &map[string]float64{
					variationD: 70,
					variationC: 10,
					variationB: 20,
				},
				Query: new(
					`{"==": [{"var": "key"}, "96ac59e6-7492-436b-b15a-ba1d797d2423"]}`,
				),
			},
			args: ruleEvaluateArgs{
				user: ffcontext.NewEvaluationContext(userKeyUUID),
			},
			wantErr: assert.NoError,
			want:    variationB,
		},
		{
			name: "Invalid JsonLogic logic rule",
			rule: flag.Rule{
				Name: new(fixtures.RuleOne),
				Percentages: &map[string]float64{
					variationD: 70,
					variationC: 10,
					variationB: 20,
				},
				Query: new(
					`{"=": [{"var": "key"}, "96ac59e6-7492-436b-b15a-ba1d797d2423"]}`,
				),
			},
			args: ruleEvaluateArgs{
				user: ffcontext.NewEvaluationContext(userKeyUUID),
			},
			wantErr: assert.Error,
		},
		{
			name: "Semver comparison with prerelease identifiers - higher version provided",
			rule: flag.Rule{
				Name:            new("semver_rule"),
				VariationResult: new(variationA),
				Query:           new("version gt 1.0.0-10"),
			},
			args: ruleEvaluateArgs{
				isDefault: false,
				user: ffcontext.NewEvaluationContextBuilder(fixtures.UserKeyUser).
					AddCustom("version", "1.0.0-11").
					Build(),
			},
			want:    variationA,
			wantErr: assert.NoError,
		},
		{
			name: "Semver comparison with prerelease identifiers - lower version provided",
			rule: flag.Rule{
				Name:            new("semver_rule"),
				VariationResult: new(variationA),
				Query:           new("version gt 1.0.0-10"),
			},
			args: ruleEvaluateArgs{
				isDefault: false,
				user: ffcontext.NewEvaluationContextBuilder(fixtures.UserKeyUser).
					AddCustom("version", "1.0.0-2").
					Build(),
			},
			want:    "",
			wantErr: ExpectRuleNotApplyError,
		},
	})
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
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationA),
			},
			updatedRule: flag.Rule{
				VariationResult: new(variationB),
			},
			want: flag.Rule{
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationB),
			},
		},
		{
			name: "merge rule with disable set to true",
			originalRule: flag.Rule{
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationA),
			},
			updatedRule: flag.Rule{
				Disable: new(true),
			},
			want: flag.Rule{
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationA),
				Disable:         new(true),
			},
		},
		{
			name: "merge rule with disable set to false",
			originalRule: flag.Rule{
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationA),
				Disable:         new(true),
			},
			updatedRule: flag.Rule{
				Disable: new(false),
			},
			want: flag.Rule{
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationA),
				Disable:         new(false),
			},
		},
		{
			name: "merge percentage",
			originalRule: flag.Rule{
				Name: new(fixtures.RuleOne),
				Percentages: &map[string]float64{
					variationA: 10,
					variationB: 10,
					variationC: 10,
					variationD: 10,
					variationE: 60,
				},
			},
			updatedRule: flag.Rule{
				Percentages: &map[string]float64{
					variationD:    -1,
					variationE:    50,
					"variation_F": 20,
				},
			},
			want: flag.Rule{
				Name: new(fixtures.RuleOne),
				Percentages: &map[string]float64{
					variationA:    10,
					variationB:    10,
					variationC:    10,
					variationE:    50,
					"variation_F": 20,
				},
			},
		},
		{
			name: "merge rule with query",
			originalRule: flag.Rule{
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationA),
				Query:           new(keyEqAbc),
			},
			updatedRule: flag.Rule{
				Query: new("key eq \"cde\""),
			},
			want: flag.Rule{
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationA),
				Query:           new("key eq \"cde\""),
			},
		},
		{
			name: "merge rule remove query",
			originalRule: flag.Rule{
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationA),
				Query:           new(keyEqAbc),
			},
			updatedRule: flag.Rule{
				Query: new(""),
			},
			want: flag.Rule{
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationA),
				Query:           new(""),
			},
		},
		{
			name: "merge rule with progressive rollout no rollout before",
			originalRule: flag.Rule{
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationA),
			},
			updatedRule: flag.Rule{
				ProgressiveRollout: &flag.ProgressiveRollout{
					Initial: &flag.ProgressiveRolloutStep{
						Variation: new(variationB),
						Date: new(
							time.Date(2021, time.February, 1, 10, 10, 10, 10, time.UTC),
						),
					},
					End: &flag.ProgressiveRolloutStep{
						Variation: new(variationC),
						Date: new(
							time.Date(2021, time.February, 3, 10, 10, 10, 10, time.UTC),
						),
					},
				},
			},
			want: flag.Rule{
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationA),
				ProgressiveRollout: &flag.ProgressiveRollout{
					Initial: &flag.ProgressiveRolloutStep{
						Variation: new(variationB),
						Date: new(
							time.Date(2021, time.February, 1, 10, 10, 10, 10, time.UTC),
						),
					},
					End: &flag.ProgressiveRolloutStep{
						Variation: new(variationC),
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
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationA),
				ProgressiveRollout: &flag.ProgressiveRollout{
					Initial: &flag.ProgressiveRolloutStep{
						Variation: new(variationB),
						Date: new(
							time.Date(2021, time.February, 1, 10, 10, 10, 10, time.UTC),
						),
					},
					End: &flag.ProgressiveRolloutStep{
						Variation: new(variationC),
						Date: new(
							time.Date(2021, time.February, 3, 10, 10, 10, 10, time.UTC),
						),
					},
				},
			},
			updatedRule: flag.Rule{
				ProgressiveRollout: &flag.ProgressiveRollout{
					Initial: &flag.ProgressiveRolloutStep{
						Variation:  new(variationD),
						Percentage: new(float64(40)),
						Date: new(
							time.Date(2021, time.February, 1, 10, 10, 10, 10, time.UTC),
						),
					},
				},
			},
			want: flag.Rule{
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationA),
				ProgressiveRollout: &flag.ProgressiveRollout{
					Initial: &flag.ProgressiveRolloutStep{
						Variation:  new(variationD),
						Percentage: new(float64(40)),
						Date: new(
							time.Date(2021, time.February, 1, 10, 10, 10, 10, time.UTC),
						),
					},
					End: &flag.ProgressiveRolloutStep{
						Variation: new(variationC),
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
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationA),
				ProgressiveRollout: &flag.ProgressiveRollout{
					Initial: &flag.ProgressiveRolloutStep{
						Variation: new(variationB),
						Date: new(
							time.Date(2021, time.February, 1, 10, 10, 10, 10, time.UTC),
						),
					},
					End: &flag.ProgressiveRolloutStep{
						Variation: new(variationC),
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
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationA),
				ProgressiveRollout: &flag.ProgressiveRollout{
					Initial: &flag.ProgressiveRolloutStep{
						Variation: new(variationB),
						Date: new(
							time.Date(2021, time.February, 1, 10, 10, 10, 10, time.UTC),
						),
					},
					End: &flag.ProgressiveRolloutStep{
						Variation: new(variationC),
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

package flagdef_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
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
	rule    flagdef.Rule
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

			var userCtx ffcontext.Context
			if tt.args.user != nil {
				userCtx = atFixedNow(tt.args.user)
			}

			got, err := tt.rule.Evaluate(
				flagdef.NewService(),
				key,
				userCtx,
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
			rule: flagdef.Rule{
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationA),
			},
			args: ruleEvaluateArgs{
				user: ffcontext.NewEvaluationContext(fixtures.UserKeyAbc),
			},
			want:    variationA,
			wantErr: assert.NoError,
		},
		{
			name: "Ignore query if default variation result",
			rule: flagdef.Rule{
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationA),
				Query:           new("key eq \"def\""),
			},
			args: ruleEvaluateArgs{
				isDefault: true,
				user:      ffcontext.NewEvaluationContext(fixtures.UserKeyAbc),
			},
			want:    variationA,
			wantErr: assert.NoError,
		},
		{
			name: "User does not match the query",
			rule: flagdef.Rule{
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationA),
				Query:           new("key eq \"def\""),
			},
			args: ruleEvaluateArgs{
				isDefault: false,
				user:      ffcontext.NewEvaluationContext(fixtures.UserKeyAbc),
			},
			wantErr: assert.Error,
		},
		{
			name: "User does not match the query (jsonlogic)",
			rule: flagdef.Rule{
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationA),
				Query:           new(`{"==": [{"var": "key"}, "def"]}`),
			},
			args: ruleEvaluateArgs{
				isDefault: false,
				user:      ffcontext.NewEvaluationContext(fixtures.UserKeyAbc),
			},
			wantErr: assert.Error,
		},
		{
			name: "User match the query",
			rule: flagdef.Rule{
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationA),
				Query:           new(keyEqAbc),
			},
			args: ruleEvaluateArgs{
				isDefault: false,
				user:      ffcontext.NewEvaluationContext(fixtures.UserKeyAbc),
			},
			want:    variationA,
			wantErr: assert.NoError,
		},
		{
			name: "JSONLogic rule return string 'true'",
			rule: flagdef.Rule{
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationA),
				Query:           new("{\"cat\": [\"t\", \"r\", \"u\", \"e\"]}"),
			},
			args: ruleEvaluateArgs{
				isDefault: false,
				user:      ffcontext.NewEvaluationContext(fixtures.UserKeyAbc),
			},
			want:    variationA,
			wantErr: assert.NoError,
		},

		{
			name: "User match the query (jsonlogic)",
			rule: flagdef.Rule{
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationA),
				Query:           new(`{"==": [{"var": "key"}, "abc"]}`),
			},
			args: ruleEvaluateArgs{
				isDefault: false,
				user:      ffcontext.NewEvaluationContext(fixtures.UserKeyAbc),
			},
			want:    variationA,
			wantErr: assert.NoError,
		},
		{
			// GHSA-mw4w-7w23-cg45: a context value shaped like JSON Logic must not be executed.
			name: "JSONLogic does not execute logic coming from the context",
			rule: flagdef.Rule{
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationA),
				Query:           new(`{"some":[{"var":"orgs"},{"==":[{"var":"plan"},"enterprise"]}]}`),
			},
			args: ruleEvaluateArgs{
				isDefault: false,
				user: ffcontext.NewEvaluationContextBuilder(fixtures.UserKeyAbc).
					AddCustom("orgs", []any{map[string]any{"id": 1}}).
					AddCustom(fixtures.AttrPlan, map[string]any{"cat": []any{"enter", "prise"}}).
					Build(),
			},
			wantErr: assert.Error,
		},
		{
			name: "Invalid json for query",
			rule: flagdef.Rule{
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationA),
				Query:           new(`{"==": [{"var": "key"}, "abc"]`),
			},
			args: ruleEvaluateArgs{
				isDefault: false,
				user:      ffcontext.NewEvaluationContext(fixtures.UserKeyAbc),
			},
			wantErr: assert.Error,
		},
		{
			name: "Invalid jsonlogic query (valid JSON but invalid query format)",
			rule: flagdef.Rule{
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationA),
				Query:           new(`{"xxx": [{"var": "key"}, "abc"]}`),
			},
			args: ruleEvaluateArgs{
				isDefault: false,
				user:      ffcontext.NewEvaluationContext(fixtures.UserKeyAbc),
			},
			wantErr: assert.Error,
		},
		{
			name: "No match and no default variation",
			rule: flagdef.Rule{
				Name: new(fixtures.RuleOne),
			},
			args:    ruleEvaluateArgs{},
			wantErr: assert.Error,
		},
		{
			name: "Percentage ignore variation result",
			rule: flagdef.Rule{
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
			rule: flagdef.Rule{
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
			rule: flagdef.Rule{
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
			rule: flagdef.Rule{
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
			rule: flagdef.Rule{
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
			rule: flagdef.Rule{
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
			rule: flagdef.Rule{
				Name: new(fixtures.RuleOne),
				Percentages: &map[string]float64{
					variationA: 100,
				},
				ProgressiveRollout: &flagdef.ProgressiveRollout{
					Initial: &flagdef.ProgressiveRolloutStep{
						Variation:  new(variationB),
						Percentage: new(float64(0)),
						// Far-future dates so evaluation always lands before the
						// ramp and deterministically serves the initial variation
						// (avoids a timing race on loaded CI runners).
						Date: new(fixedNow().Add(1 * time.Hour)),
					},
					End: &flagdef.ProgressiveRolloutStep{
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
			rule: flagdef.Rule{
				Name: new(fixtures.RuleOne),
				ProgressiveRollout: &flagdef.ProgressiveRollout{
					Initial: &flagdef.ProgressiveRolloutStep{
						Variation:  new(variationB),
						Percentage: new(float64(0)),
						Date:       new(fixedNow().Add(1 * time.Second)),
					},
					End: &flagdef.ProgressiveRolloutStep{
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
			rule: flagdef.Rule{
				Name: new(fixtures.RuleOne),
				ProgressiveRollout: &flagdef.ProgressiveRollout{
					Initial: &flagdef.ProgressiveRolloutStep{
						Variation:  new(variationB),
						Percentage: new(float64(0)),
						Date:       new(fixedNow().Add(-6 * time.Second)),
					},
					End: &flagdef.ProgressiveRolloutStep{
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
			rule: flagdef.Rule{
				Name: new(fixtures.RuleOne),
				ProgressiveRollout: &flagdef.ProgressiveRollout{
					Initial: &flagdef.ProgressiveRolloutStep{
						Variation:  new(variationB),
						Percentage: new(float64(0)),
						Date:       new(fixedNow().Add(-1 * time.Second)),
					},
					End: &flagdef.ProgressiveRolloutStep{
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
			rule: flagdef.Rule{
				Name: new(fixtures.RuleOne),
				ProgressiveRollout: &flagdef.ProgressiveRollout{
					Initial: &flagdef.ProgressiveRolloutStep{
						Variation:  new(variationB),
						Percentage: new(float64(0)),
						Date:       new(fixedNow().Add(-3 * time.Second)),
					},
					End: &flagdef.ProgressiveRolloutStep{
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
			rule: flagdef.Rule{
				Name: new(fixtures.RuleOne),
				ProgressiveRollout: &flagdef.ProgressiveRollout{
					Initial: &flagdef.ProgressiveRolloutStep{
						Variation: new(variationB),
						Date:      new(fixedNow().Add(-3 * time.Second)),
					},
					End: &flagdef.ProgressiveRolloutStep{
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
			rule: flagdef.Rule{
				Name: new(fixtures.RuleOne),
				ProgressiveRollout: &flagdef.ProgressiveRollout{
					Initial: &flagdef.ProgressiveRolloutStep{
						Variation:  new(variationB),
						Percentage: new(float64(0)),
						Date:       new(fixedNow().Add(3 * time.Second)),
					},
					End: &flagdef.ProgressiveRolloutStep{
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
			rule: flagdef.Rule{
				Name: new(fixtures.RuleOne),
				ProgressiveRollout: &flagdef.ProgressiveRollout{
					End: &flagdef.ProgressiveRolloutStep{
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
			rule: flagdef.Rule{
				Name: new(fixtures.RuleOne),
				ProgressiveRollout: &flagdef.ProgressiveRollout{
					Initial: &flagdef.ProgressiveRolloutStep{
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
			rule: flagdef.Rule{
				Name: new(fixtures.RuleOne),
				ProgressiveRollout: &flagdef.ProgressiveRollout{
					Initial: &flagdef.ProgressiveRolloutStep{
						Percentage: new(float64(0)),
						Date:       new(fixedNow().Add(3 * time.Second)),
					},
					End: &flagdef.ProgressiveRolloutStep{
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
			rule: flagdef.Rule{
				Name: new(fixtures.RuleOne),
				ProgressiveRollout: &flagdef.ProgressiveRollout{
					Initial: &flagdef.ProgressiveRolloutStep{
						Variation:  new(variationB),
						Percentage: new(float64(0)),
						Date:       new(fixedNow().Add(3 * time.Second)),
					},
					End: &flagdef.ProgressiveRolloutStep{
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
			rule: flagdef.Rule{
				Name: new(fixtures.RuleOne),
				ProgressiveRollout: &flagdef.ProgressiveRollout{
					Initial: &flagdef.ProgressiveRolloutStep{
						Variation:  new(variationB),
						Percentage: new(float64(0)),
					},
					End: &flagdef.ProgressiveRolloutStep{
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
			rule: flagdef.Rule{
				Name: new(fixtures.RuleOne),
				ProgressiveRollout: &flagdef.ProgressiveRollout{
					Initial: &flagdef.ProgressiveRolloutStep{
						Variation:  new(variationB),
						Percentage: new(float64(0)),
						Date:       new(fixedNow().Add(1 * time.Second)),
					},
					End: &flagdef.ProgressiveRolloutStep{
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
			rule: flagdef.Rule{
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationA),
				Query:           new(`{"==": [{"var": "key"}, "def"]}`),
			},
			args: ruleEvaluateArgs{
				isDefault: false,
				user:      ffcontext.NewEvaluationContext(fixtures.UserKeyAbc),
			},
			wantErr: assert.Error,
		},
		{
			name: "User partially matches with in array operator (JsonLogic)",
			rule: flagdef.Rule{
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationA),
				Query:           new(`{"in": [{"var": "key"}, ["abcdef", "1234"]]}`),
			},
			args: ruleEvaluateArgs{
				isDefault: false,
				user:      ffcontext.NewEvaluationContext(fixtures.UserKeyAbc),
			},
			wantErr: assert.Error,
		},
		{
			name: "User match the query (JsonLogic)",
			rule: flagdef.Rule{
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationA),
				Query:           new(`{"==": [{"var": "key"}, "abc"]}`),
			},
			args: ruleEvaluateArgs{
				isDefault: false,
				user:      ffcontext.NewEvaluationContext(fixtures.UserKeyAbc),
			},
			want:    variationA,
			wantErr: assert.NoError,
		},
		{
			name: "Percentage + user match query (JsonLogic)",
			rule: flagdef.Rule{
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
			rule: flagdef.Rule{
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
			rule: flagdef.Rule{
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
			rule: flagdef.Rule{
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
		originalRule flagdef.Rule
		updatedRule  flagdef.Rule
		want         flagdef.Rule
	}{
		{
			name: "merge simple rule",
			originalRule: flagdef.Rule{
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationA),
			},
			updatedRule: flagdef.Rule{
				VariationResult: new(variationB),
			},
			want: flagdef.Rule{
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationB),
			},
		},
		{
			name: "merge rule with disable set to true",
			originalRule: flagdef.Rule{
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationA),
			},
			updatedRule: flagdef.Rule{
				Disable: new(true),
			},
			want: flagdef.Rule{
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationA),
				Disable:         new(true),
			},
		},
		{
			name: "merge rule with disable set to false",
			originalRule: flagdef.Rule{
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationA),
				Disable:         new(true),
			},
			updatedRule: flagdef.Rule{
				Disable: new(false),
			},
			want: flagdef.Rule{
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationA),
				Disable:         new(false),
			},
		},
		{
			name: "merge percentage",
			originalRule: flagdef.Rule{
				Name: new(fixtures.RuleOne),
				Percentages: &map[string]float64{
					variationA: 10,
					variationB: 10,
					variationC: 10,
					variationD: 10,
					variationE: 60,
				},
			},
			updatedRule: flagdef.Rule{
				Percentages: &map[string]float64{
					variationD:    -1,
					variationE:    50,
					"variation_F": 20,
				},
			},
			want: flagdef.Rule{
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
			originalRule: flagdef.Rule{
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationA),
				Query:           new(keyEqAbc),
			},
			updatedRule: flagdef.Rule{
				Query: new("key eq \"cde\""),
			},
			want: flagdef.Rule{
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationA),
				Query:           new("key eq \"cde\""),
			},
		},
		{
			name: "merge rule remove query",
			originalRule: flagdef.Rule{
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationA),
				Query:           new(keyEqAbc),
			},
			updatedRule: flagdef.Rule{
				Query: new(""),
			},
			want: flagdef.Rule{
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationA),
				Query:           new(""),
			},
		},
		{
			name: "merge rule with progressive rollout no rollout before",
			originalRule: flagdef.Rule{
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationA),
			},
			updatedRule: flagdef.Rule{
				ProgressiveRollout: &flagdef.ProgressiveRollout{
					Initial: &flagdef.ProgressiveRolloutStep{
						Variation: new(variationB),
						Date: new(
							time.Date(2021, time.February, 1, 10, 10, 10, 10, time.UTC),
						),
					},
					End: &flagdef.ProgressiveRolloutStep{
						Variation: new(variationC),
						Date: new(
							time.Date(2021, time.February, 3, 10, 10, 10, 10, time.UTC),
						),
					},
				},
			},
			want: flagdef.Rule{
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationA),
				ProgressiveRollout: &flagdef.ProgressiveRollout{
					Initial: &flagdef.ProgressiveRolloutStep{
						Variation: new(variationB),
						Date: new(
							time.Date(2021, time.February, 1, 10, 10, 10, 10, time.UTC),
						),
					},
					End: &flagdef.ProgressiveRolloutStep{
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
			originalRule: flagdef.Rule{
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationA),
				ProgressiveRollout: &flagdef.ProgressiveRollout{
					Initial: &flagdef.ProgressiveRolloutStep{
						Variation: new(variationB),
						Date: new(
							time.Date(2021, time.February, 1, 10, 10, 10, 10, time.UTC),
						),
					},
					End: &flagdef.ProgressiveRolloutStep{
						Variation: new(variationC),
						Date: new(
							time.Date(2021, time.February, 3, 10, 10, 10, 10, time.UTC),
						),
					},
				},
			},
			updatedRule: flagdef.Rule{
				ProgressiveRollout: &flagdef.ProgressiveRollout{
					Initial: &flagdef.ProgressiveRolloutStep{
						Variation:  new(variationD),
						Percentage: new(float64(40)),
						Date: new(
							time.Date(2021, time.February, 1, 10, 10, 10, 10, time.UTC),
						),
					},
				},
			},
			want: flagdef.Rule{
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationA),
				ProgressiveRollout: &flagdef.ProgressiveRollout{
					Initial: &flagdef.ProgressiveRolloutStep{
						Variation:  new(variationD),
						Percentage: new(float64(40)),
						Date: new(
							time.Date(2021, time.February, 1, 10, 10, 10, 10, time.UTC),
						),
					},
					End: &flagdef.ProgressiveRolloutStep{
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
			originalRule: flagdef.Rule{
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationA),
				ProgressiveRollout: &flagdef.ProgressiveRollout{
					Initial: &flagdef.ProgressiveRolloutStep{
						Variation: new(variationB),
						Date: new(
							time.Date(2021, time.February, 1, 10, 10, 10, 10, time.UTC),
						),
					},
					End: &flagdef.ProgressiveRolloutStep{
						Variation: new(variationC),
						Date: new(
							time.Date(2021, time.February, 3, 10, 10, 10, 10, time.UTC),
						),
					},
				},
			},
			updatedRule: flagdef.Rule{
				ProgressiveRollout: &flagdef.ProgressiveRollout{
					End: &flagdef.ProgressiveRolloutStep{
						Date: new(
							time.Date(2021, time.February, 12, 10, 10, 10, 10, time.UTC),
						),
						Percentage: new(float64(100)),
					},
				},
			},
			want: flagdef.Rule{
				Name:            new(fixtures.RuleOne),
				VariationResult: new(variationA),
				ProgressiveRollout: &flagdef.ProgressiveRollout{
					Initial: &flagdef.ProgressiveRolloutStep{
						Variation: new(variationB),
						Date: new(
							time.Date(2021, time.February, 1, 10, 10, 10, 10, time.UTC),
						),
					},
					End: &flagdef.ProgressiveRolloutStep{
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

package flag_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/flag"
	"github.com/blackbirdworks/semaphore/modules/core/testhelpers/clocktest"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

const (
	companyID                     = "company.id"
	devEnvironment                = "DEV environment"
	defaultSdk                    = "default-sdk"
	environmentKey                = "environment"
	errorInTheConfiguration       = "error in the configuration, no variation available for this rule"
	httpsIssueLinkGOFF            = "https://issue.link/GOFF-1"
	invalidPercentagesShouldNot   = "invalid percentages: should not be empty"
	invalidPercentagesShouldNotBe = "invalid percentages: should not be equal to 0"
	keyEqFive                     = "key eq 5"
	keyEqNotUser                  = "key eq \"not-user-key\""
	keyEqUserKey                  = "key eq \"user-key\""
	notExpectedValueS             = "not expected value: %s"
	nothingToReturn               = "nothing to return"
	ruleOne                       = "Rule1"
	ruleA                         = "rule-a"
	sdkDefault                    = "SdkDefault"
	semaphore                     = "semaphore"
	targetingKeyEqOne             = "targetingKey eq 1"
	teamID                        = "teamId"
	testRule                      = "test-rule"
	thisIsAFlag                   = "this is a flag"
	valueA                        = "valueA"
	valueAUnderscore              = "value_A"
	valueB                        = "valueB"
	valueBUnderscore              = "value_B"
	valueC                        = "value_C"
	valueD                        = "value_D"
	valueDefault                  = "value_default"
	valueQWERTY                   = "value_QWERTY"
	varAName                      = "varA"
	varBName                      = "varB"
	varEnvironmentUUID0Ffcc       = `{"===":[{"var":"EnvironmentUUID"},"0ffcc8da-1f70-4229-8ef7-b5a3130cdd52"]}`
	varKeyNotUser                 = `{"==":[{"var":"key"},"not-user-key"]}`
	varKeyUserKey                 = `{"==":[{"var":"key"},"user-key"]}`
	variationA                    = "variation_A"
	variationB                    = "variation_B"
	variationC                    = "variation_C"
	variationD                    = "variation_D"
	fixtureFalse                  = "fixtureFalse"
)

type internalFlagValueArgs struct {
	flagContext flag.Context
	user        ffcontext.Context
	flagName    string
}

type internalFlagValueCase struct {
	flag  flag.InternalFlag
	want  any
	args  internalFlagValueArgs
	name  string
	want1 flag.ResolutionDetails
}

type internalFlagValidFields struct {
	Variations      *map[string]*any
	Rules           *[]flag.Rule
	DefaultRule     *flag.Rule
	Rollout         *flag.Rollout
	TrackEvents     *bool
	Disable         *bool
	Version         *string
	Experimentation *flag.ExperimentationRollout
	Scheduled       *[]flag.ScheduledStep
	Metadata        *map[string]any
}

type internalFlagValidCase struct {
	name     string
	fields   internalFlagValidFields
	wantErr  assert.ErrorAssertionFunc
	errorMsg string
}

func runInternalFlagValueTests(t *testing.T, tests []internalFlagValueCase) {
	t.Helper()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, got1 := tt.flag.Value(tt.args.flagName, atFixedNow(tt.args.user), tt.args.flagContext)
			assert.Equalf(t, tt.want, got, notExpectedValueS, cmp.Diff(tt.want, got))
			assert.Equalf(t, tt.want1, got1, notExpectedValueS, cmp.Diff(tt.want1, got1))
		})
	}
}

func runInternalFlagValidTests(t *testing.T, tests []internalFlagValidCase) {
	t.Helper()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			internalFlag := &flag.InternalFlag{
				Variations:      tt.fields.Variations,
				Rules:           tt.fields.Rules,
				DefaultRule:     tt.fields.DefaultRule,
				TrackEvents:     tt.fields.TrackEvents,
				Disable:         tt.fields.Disable,
				Version:         tt.fields.Version,
				Scheduled:       tt.fields.Scheduled,
				Experimentation: tt.fields.Experimentation,
			}
			err := internalFlag.IsValid(flag.NewService())

			errMsg := ""
			if err != nil {
				errMsg = err.Error()
			}

			tt.wantErr(t, err, fmt.Sprintf("IsValid(): %s", err))
			assert.Equal(t, tt.errorMsg, errMsg)
		})
	}
}

func TestInternalFlag_Value(t *testing.T) {
	t.Parallel()

	runInternalFlagValueTests(t, []internalFlagValueCase{
		{
			name: "Should use default value if no targeting",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					variationA: new(any(true)),
					variationB: new(any(false)),
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(variationA),
				},
				Metadata: &map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
			args: internalFlagValueArgs{
				flagName: fixtures.FlagKeyMy,
				user:     ffcontext.NewEvaluationContext(fixtures.UserKeyUser),
				flagContext: flag.Context{Service: flag.NewService(),
					DefaultSdkValue: false,
				},
			},
			want: true,
			want1: flag.ResolutionDetails{
				Variant:   variationA,
				Reason:    flag.ReasonStatic,
				Cacheable: true,
				Metadata: map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
		},
		{
			name: "Should use default value if percentages are empty",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					variationA: new(any(true)),
					variationB: new(any(false)),
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(variationA),
					Percentages:     &map[string]float64{},
				},
				Metadata: &map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
			args: internalFlagValueArgs{
				flagName: fixtures.FlagKeyMy,
				user:     ffcontext.NewEvaluationContext(fixtures.UserKeyUser),
				flagContext: flag.Context{Service: flag.NewService(),
					DefaultSdkValue: false,
				},
			},
			want: true,
			want1: flag.ResolutionDetails{
				Variant:   variationA,
				Reason:    flag.ReasonStatic,
				Cacheable: true,
				Metadata: map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
		},
		{
			name: "Should return sdk default value when flag is disabled",
			flag: flag.InternalFlag{
				Disable: new(true),
				Metadata: &map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
			args: internalFlagValueArgs{
				flagName: fixtures.FlagKeyMy,
				user:     ffcontext.NewEvaluationContext(fixtures.UserKeyUser),
				flagContext: flag.Context{Service: flag.NewService(),
					DefaultSdkValue: defaultSdk,
				},
			},
			want: defaultSdk,
			want1: flag.ResolutionDetails{
				Variant:   sdkDefault,
				Reason:    flag.ReasonDisabled,
				Cacheable: true,
				Metadata: map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
		},
		{
			name: "Should return sdk default value when experimentation rollout not started",
			flag: flag.InternalFlag{
				Experimentation: &flag.ExperimentationRollout{
					Start: new(fixedNow().Add(1 * time.Second)),
					End:   new(fixedNow().Add(5 * time.Second)),
				},
				Metadata: &map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
			args: internalFlagValueArgs{
				flagName: fixtures.FlagKeyMy,
				user:     ffcontext.NewEvaluationContext(fixtures.UserKeyUser),
				flagContext: flag.Context{Service: flag.NewService(),
					DefaultSdkValue: defaultSdk,
				},
			},
			want: defaultSdk,
			want1: flag.ResolutionDetails{
				Variant:   sdkDefault,
				Reason:    flag.ReasonDisabled,
				Cacheable: false,
				Metadata: map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
		},
		{
			name: "Should return sdk default value when experimentation rollout is now, but context date override",
			flag: flag.InternalFlag{
				Experimentation: &flag.ExperimentationRollout{
					Start: new(fixedNow().Add(-5 * time.Second)),
					End:   new(fixedNow().Add(5 * time.Second)),
				},
				Metadata: &map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
			args: internalFlagValueArgs{
				flagName: fixtures.FlagKeyMy,
				user: ffcontext.NewEvaluationContextBuilder(fixtures.UserKeyUser).
					AddCustom(fixtures.GoFeatureFlagOrg, map[string]any{
						fixtures.CurrentDateTime: fixedNow().Add(6 * time.Second).Format(time.RFC3339),
					}).
					Build(),
				flagContext: flag.Context{Service: flag.NewService(),
					DefaultSdkValue: defaultSdk,
				},
			},
			want: defaultSdk,
			want1: flag.ResolutionDetails{
				Variant:   sdkDefault,
				Reason:    flag.ReasonDisabled,
				Cacheable: false,
				Metadata: map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
		},
		{
			name: "Should return sdk default value when experimentation rollout ended",
			flag: flag.InternalFlag{
				Experimentation: &flag.ExperimentationRollout{
					Start: new(fixedNow().Add(-15 * time.Second)),
					End:   new(fixedNow().Add(-5 * time.Second)),
				},
				Metadata: &map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
			args: internalFlagValueArgs{
				flagName: fixtures.FlagKeyMy,
				user:     ffcontext.NewEvaluationContext(fixtures.UserKeyUser),
				flagContext: flag.Context{Service: flag.NewService(),
					DefaultSdkValue: defaultSdk,
				},
			},
			want: defaultSdk,
			want1: flag.ResolutionDetails{
				Variant:   sdkDefault,
				Reason:    flag.ReasonDisabled,
				Cacheable: false,
				Metadata: map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
		},
	})
}

func TestInternalFlag_Value_Targeting(t *testing.T) {
	t.Parallel()

	runInternalFlagValueTests(t, []internalFlagValueCase{
		{
			name: "Should return the variation specified in the rule if rule match",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					variationA: new(any(true)),
					variationB: new(any(false)),
				},
				Rules: &[]flag.Rule{
					{
						Name:            new(fixtures.RuleOne),
						Query:           new(keyEqUserKey),
						VariationResult: new(variationB),
					},
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(variationA),
				},
				Metadata: &map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
			args: internalFlagValueArgs{
				flagName: fixtures.FlagKeyMy,
				user:     ffcontext.NewEvaluationContext(fixtures.UserKeyUser),
				flagContext: flag.Context{Service: flag.NewService(),
					DefaultSdkValue: false,
				},
			},
			want: false,
			want1: flag.ResolutionDetails{
				Variant:   variationB,
				Reason:    flag.ReasonTargetingMatch,
				RuleIndex: new(0),
				RuleName:  new(fixtures.RuleOne),
				Cacheable: true,
				Metadata: map[string]any{
					fixtures.Description:       thisIsAFlag,
					fixtures.IssueLink:         httpsIssueLinkGOFF,
					fixtures.EvaluatedRuleName: fixtures.RuleOne,
				},
			},
		},
		{
			name: "Should return the variation specified in the rule if rule match (jsonLogic)",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					variationA: new(any(true)),
					variationB: new(any(false)),
				},
				Rules: &[]flag.Rule{
					{
						Name:            new(fixtures.RuleOne),
						Query:           new(varKeyUserKey),
						VariationResult: new(variationB),
					},
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(variationA),
				},
				Metadata: &map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
			args: internalFlagValueArgs{
				flagName: fixtures.FlagKeyMy,
				user:     ffcontext.NewEvaluationContext(fixtures.UserKeyUser),
				flagContext: flag.Context{Service: flag.NewService(),
					DefaultSdkValue: false,
				},
			},
			want: false,
			want1: flag.ResolutionDetails{
				Variant:   variationB,
				Reason:    flag.ReasonTargetingMatch,
				RuleIndex: new(0),
				RuleName:  new(fixtures.RuleOne),
				Cacheable: true,
				Metadata: map[string]any{
					fixtures.Description:       thisIsAFlag,
					fixtures.IssueLink:         httpsIssueLinkGOFF,
					fixtures.EvaluatedRuleName: fixtures.RuleOne,
				},
			},
		},
		{
			name: "Should match the 2nd rule",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					variationA: new(any(valueAUnderscore)),
					variationB: new(any(valueBUnderscore)),
					variationC: new(any(valueC)),
				},
				Rules: &[]flag.Rule{
					{
						Name:            new(fixtures.RuleOne),
						Query:           new(keyEqNotUser),
						VariationResult: new(variationC),
					},
					{
						Name:            new(fixtures.RuleTwo),
						Query:           new(keyEqUserKey),
						VariationResult: new(variationC),
					},
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(variationA),
				},
				Metadata: &map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
			args: internalFlagValueArgs{
				flagName: fixtures.FlagKeyMy,
				user:     ffcontext.NewEvaluationContext(fixtures.UserKeyUser),
				flagContext: flag.Context{Service: flag.NewService(),
					DefaultSdkValue: valueDefault,
				},
			},
			want: valueC,
			want1: flag.ResolutionDetails{
				Variant:   variationC,
				Reason:    flag.ReasonTargetingMatch,
				RuleIndex: new(1),
				RuleName:  new(fixtures.RuleTwo),
				Cacheable: true,
				Metadata: map[string]any{
					fixtures.Description:       thisIsAFlag,
					fixtures.IssueLink:         httpsIssueLinkGOFF,
					fixtures.EvaluatedRuleName: fixtures.RuleTwo,
				},
			},
		},
		{
			name: "Should match the 2nd rule (jsonLogic)",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					variationA: new(any(valueAUnderscore)),
					variationB: new(any(valueBUnderscore)),
					variationC: new(any(valueC)),
				},
				Rules: &[]flag.Rule{
					{
						Name: new(fixtures.RuleOne),
						Query: new(
							varKeyNotUser,
						),
						VariationResult: new(variationC),
					},
					{
						Name:            new(fixtures.RuleTwo),
						Query:           new(varKeyUserKey),
						VariationResult: new(variationC),
					},
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(variationA),
				},
				Metadata: &map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
			args: internalFlagValueArgs{
				flagName: fixtures.FlagKeyMy,
				user:     ffcontext.NewEvaluationContext(fixtures.UserKeyUser),
				flagContext: flag.Context{Service: flag.NewService(),
					DefaultSdkValue: valueDefault,
				},
			},
			want: valueC,
			want1: flag.ResolutionDetails{
				Variant:   variationC,
				Reason:    flag.ReasonTargetingMatch,
				RuleIndex: new(1),
				RuleName:  new(fixtures.RuleTwo),
				Cacheable: true,
				Metadata: map[string]any{
					fixtures.Description:       thisIsAFlag,
					fixtures.IssueLink:         httpsIssueLinkGOFF,
					fixtures.EvaluatedRuleName: fixtures.RuleTwo,
				},
			},
		},
	})
}

func TestInternalFlag_Value_UnnamedAndFirstRule(t *testing.T) {
	t.Parallel()

	runInternalFlagValueTests(t, []internalFlagValueCase{
		{
			name: "Should match a rule with no name",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					variationA: new(any(valueAUnderscore)),
					variationB: new(any(valueBUnderscore)),
					variationC: new(any(valueC)),
				},
				Rules: &[]flag.Rule{
					{
						Query:           new(keyEqNotUser),
						VariationResult: new(variationC),
					},
					{
						Query:           new(keyEqUserKey),
						VariationResult: new(variationC),
					},
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(variationA),
				},
				Metadata: &map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
			args: internalFlagValueArgs{
				flagName: fixtures.FlagKeyMy,
				user:     ffcontext.NewEvaluationContext(fixtures.UserKeyUser),
				flagContext: flag.Context{Service: flag.NewService(),
					DefaultSdkValue: valueDefault,
				},
			},
			want: valueC,
			want1: flag.ResolutionDetails{
				Variant:   variationC,
				Reason:    flag.ReasonTargetingMatch,
				RuleIndex: new(1),
				Cacheable: true,
				Metadata: map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
		},
		{
			name: "Should match a rule with no name (jsonLogic)",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					variationA: new(any(valueAUnderscore)),
					variationB: new(any(valueBUnderscore)),
					variationC: new(any(valueC)),
				},
				Rules: &[]flag.Rule{
					{
						Query: new(
							varKeyNotUser,
						),
						VariationResult: new(variationC),
					},
					{
						Query:           new(varKeyUserKey),
						VariationResult: new(variationC),
					},
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(variationA),
				},
				Metadata: &map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
			args: internalFlagValueArgs{
				flagName: fixtures.FlagKeyMy,
				user:     ffcontext.NewEvaluationContext(fixtures.UserKeyUser),
				flagContext: flag.Context{Service: flag.NewService(),
					DefaultSdkValue: valueDefault,
				},
			},
			want: valueC,
			want1: flag.ResolutionDetails{
				Variant:   variationC,
				Reason:    flag.ReasonTargetingMatch,
				RuleIndex: new(1),
				Cacheable: true,
				Metadata: map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
		},
		{
			name: "Should match only the first rule that apply (even if more than one can apply)",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					variationA: new(any(valueAUnderscore)),
					variationB: new(any(valueBUnderscore)),
					variationC: new(any(valueC)),
					variationD: new(any(valueD)),
				},
				Rules: &[]flag.Rule{
					{
						Query:           new(keyEqNotUser),
						VariationResult: new(variationC),
					},
					{
						Query:           new("company eq \"semaphore\""),
						VariationResult: new(variationD),
					},
					{
						Query:           new(keyEqUserKey),
						VariationResult: new(variationC),
					},
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(variationA),
				},
				Metadata: &map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
			args: internalFlagValueArgs{
				flagName: fixtures.FlagKeyMy,
				user: ffcontext.NewEvaluationContextBuilder(fixtures.UserKeyUser).
					AddCustom(fixtures.UserCompany, semaphore).
					Build(),
				flagContext: flag.Context{Service: flag.NewService(),
					DefaultSdkValue: valueDefault,
				},
			},
			want: valueD,
			want1: flag.ResolutionDetails{
				Variant:   variationD,
				Reason:    flag.ReasonTargetingMatch,
				RuleIndex: new(1),
				Cacheable: true,
				Metadata: map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
		},
		{
			name: "Should match only the first rule that apply (even if more than one can apply) (jsonLogic)",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					variationA: new(any(valueAUnderscore)),
					variationB: new(any(valueBUnderscore)),
					variationC: new(any(valueC)),
					variationD: new(any(valueD)),
				},
				Rules: &[]flag.Rule{
					{
						Query: new(
							varKeyNotUser,
						),
						VariationResult: new(variationC),
					},
					{
						Query: new(
							`{"==":[{"var":"company"},"semaphore"]}`,
						),
						VariationResult: new(variationD),
					},
					{
						Query:           new(varKeyUserKey),
						VariationResult: new(variationC),
					},
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(variationA),
				},
				Metadata: &map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
			args: internalFlagValueArgs{
				flagName: fixtures.FlagKeyMy,
				user: ffcontext.NewEvaluationContextBuilder(fixtures.UserKeyUser).
					AddCustom(fixtures.UserCompany, semaphore).
					Build(),
				flagContext: flag.Context{Service: flag.NewService(),
					DefaultSdkValue: valueDefault,
				},
			},
			want: valueD,
			want1: flag.ResolutionDetails{
				Variant:   variationD,
				Reason:    flag.ReasonTargetingMatch,
				RuleIndex: new(1),
				Cacheable: true,
				Metadata: map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
		},
	})
}

func TestInternalFlag_Value_DisabledAndInvalidRules(t *testing.T) {
	t.Parallel()

	runInternalFlagValueTests(t, []internalFlagValueCase{
		{
			name: "Should ignore a rule that is disabled",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					variationA: new(any(valueAUnderscore)),
					variationB: new(any(valueBUnderscore)),
					variationC: new(any(valueC)),
					variationD: new(any(valueD)),
				},
				Rules: &[]flag.Rule{
					{
						Query:           new(keyEqNotUser),
						VariationResult: new(variationC),
					},
					{
						Query:           new("company eq \"semaphore\""),
						VariationResult: new(variationD),
						Disable:         new(true),
					},
					{
						Query:           new(keyEqUserKey),
						VariationResult: new(variationC),
					},
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(variationA),
				},
				Metadata: &map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
			args: internalFlagValueArgs{
				flagName: fixtures.FlagKeyMy,
				user: ffcontext.NewEvaluationContextBuilder(fixtures.UserKeyUser).
					AddCustom(fixtures.UserCompany, semaphore).
					Build(),
				flagContext: flag.Context{Service: flag.NewService(),
					DefaultSdkValue: valueDefault,
				},
			},
			want: valueC,
			want1: flag.ResolutionDetails{
				Variant:   variationC,
				Reason:    flag.ReasonTargetingMatch,
				RuleIndex: new(2),
				Cacheable: true,
				Metadata: map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
		},
		{
			name: "Should return an error if rule is invalid",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					variationA: new(any(valueAUnderscore)),
					variationB: new(any(valueBUnderscore)),
					variationC: new(any(valueC)),
					variationD: new(any(valueD)),
				},
				Rules: &[]flag.Rule{
					{
						Query:       new(keyEqUserKey),
						Percentages: &map[string]float64{},
					},
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(variationA),
				},
				Metadata: &map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
			args: internalFlagValueArgs{
				flagName: fixtures.FlagKeyMy,
				user:     ffcontext.NewEvaluationContextBuilder(fixtures.UserKeyUser).Build(),
				flagContext: flag.Context{Service: flag.NewService(),
					DefaultSdkValue: valueDefault,
				},
			},
			want: valueDefault,
			want1: flag.ResolutionDetails{
				Variant:   flag.VariationSDKDefault,
				Reason:    flag.ReasonError,
				ErrorCode: flag.ErrorFlagConfiguration,
				Metadata: map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
				ErrorMessage: errorInTheConfiguration,
			},
		},
		{
			name: "Should return an error if rule is invalid (jsonLogic)",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					variationA: new(any(valueAUnderscore)),
					variationB: new(any(valueBUnderscore)),
					variationC: new(any(valueC)),
					variationD: new(any(valueD)),
				},
				Rules: &[]flag.Rule{
					{
						Query:       new(varKeyUserKey),
						Percentages: &map[string]float64{},
					},
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(variationA),
				},
				Metadata: &map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
			args: internalFlagValueArgs{
				flagName: fixtures.FlagKeyMy,
				user:     ffcontext.NewEvaluationContextBuilder(fixtures.UserKeyUser).Build(),
				flagContext: flag.Context{Service: flag.NewService(),
					DefaultSdkValue: valueDefault,
				},
			},
			want: valueDefault,
			want1: flag.ResolutionDetails{
				Variant:   flag.VariationSDKDefault,
				Reason:    flag.ReasonError,
				ErrorCode: flag.ErrorFlagConfiguration,
				Metadata: map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
				ErrorMessage: errorInTheConfiguration,
			},
		},
		{
			name: "Should return an error if no targeting match and we have no default rule",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					variationA: new(any(valueAUnderscore)),
					variationB: new(any(valueBUnderscore)),
					variationC: new(any(valueC)),
					variationD: new(any(valueD)),
				},
				Rules: &[]flag.Rule{
					{
						Query: new(keyEqNotUser),
						Percentages: &map[string]float64{
							variationA: 10,
							variationB: 100,
						},
					},
				},
				Metadata: &map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
			args: internalFlagValueArgs{
				flagName: fixtures.FlagKeyMy,
				user:     ffcontext.NewEvaluationContextBuilder(fixtures.UserKeyUser).Build(),
				flagContext: flag.Context{Service: flag.NewService(),
					DefaultSdkValue: valueDefault,
				},
			},
			want: valueDefault,
			want1: flag.ResolutionDetails{
				Variant:   flag.VariationSDKDefault,
				Reason:    flag.ReasonError,
				ErrorCode: flag.ErrorFlagConfiguration,
				Metadata: map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
				ErrorMessage: "no default targeting for the flag",
			},
		},
		{
			name: "Should return an error if default rule is invalid",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					variationA: new(any(valueAUnderscore)),
					variationB: new(any(valueBUnderscore)),
					variationC: new(any(valueC)),
					variationD: new(any(valueD)),
				},
				DefaultRule: &flag.Rule{
					Percentages: &map[string]float64{},
				},
				Metadata: &map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
			args: internalFlagValueArgs{
				flagName: fixtures.FlagKeyMy,
				user:     ffcontext.NewEvaluationContextBuilder(fixtures.UserKeyUser).Build(),
				flagContext: flag.Context{Service: flag.NewService(),
					DefaultSdkValue: valueDefault,
				},
			},
			want: valueDefault,
			want1: flag.ResolutionDetails{
				Variant:   flag.VariationSDKDefault,
				Reason:    flag.ReasonError,
				ErrorCode: flag.ErrorFlagConfiguration,
				Metadata: map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
				ErrorMessage: errorInTheConfiguration,
			},
		},
	})
}

func TestInternalFlag_Value_FutureAndSingleScheduled(t *testing.T) {
	t.Parallel()

	runInternalFlagValueTests(t, []internalFlagValueCase{
		{
			name: "Should not have any change if all scheduled steps are in the future",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					variationA: new(any(valueAUnderscore)),
					variationB: new(any(valueBUnderscore)),
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(variationA),
				},
				Metadata: &map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
				Scheduled: &[]flag.ScheduledStep{
					{
						DefaultRule: &flag.Rule{
							VariationResult: new(variationB),
						},
						Date: new(fixedNow().Add(1 * time.Hour)),
					},
					{
						Variations: &map[string]*any{
							variationA: new(any(valueQWERTY)),
						},
						Date: new(fixedNow().Add(2 * time.Hour)),
					},
				},
			},
			args: internalFlagValueArgs{
				flagName: fixtures.FlagKeyMy,
				user:     ffcontext.NewEvaluationContextBuilder(fixtures.UserKeyUser).Build(),
				flagContext: flag.Context{Service: flag.NewService(),
					DefaultSdkValue: valueDefault,
				},
			},
			want: valueAUnderscore,
			want1: flag.ResolutionDetails{
				Variant: variationA,
				Reason:  flag.ReasonStatic,
				Metadata: map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
		},
		{
			name: "Should change if all scheduled steps are in the future BUT context override is in the future too",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					variationA: new(any(valueAUnderscore)),
					variationB: new(any(valueBUnderscore)),
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(variationA),
				},
				Metadata: &map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
				Scheduled: &[]flag.ScheduledStep{
					{
						DefaultRule: &flag.Rule{
							VariationResult: new(variationB),
						},
						Date: new(fixedNow().Add(1 * time.Second)),
					},
					{
						Variations: &map[string]*any{
							variationA: new(any(valueQWERTY)),
						},
						Date: new(fixedNow().Add(2 * time.Second)),
					},
				},
			},
			args: internalFlagValueArgs{
				flagName: fixtures.FlagKeyMy,
				user: ffcontext.NewEvaluationContextBuilder(fixtures.UserKeyUser).
					AddCustom(fixtures.GoFeatureFlagOrg, map[string]string{
						fixtures.CurrentDateTime: fixedNow().Add(1 * time.Minute).Format(time.RFC3339),
					}).
					Build(),
				flagContext: flag.Context{Service: flag.NewService(),
					DefaultSdkValue: valueDefault,
				},
			},
			want: valueBUnderscore,
			want1: flag.ResolutionDetails{
				Variant: variationB,
				Reason:  flag.ReasonStatic,
				Metadata: map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
		},
		{
			name: "Should only apply 1 scheduled step",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					variationA: new(any(valueAUnderscore)),
					variationB: new(any(valueBUnderscore)),
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(variationA),
				},
				Metadata: &map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
				Scheduled: &[]flag.ScheduledStep{
					{
						DefaultRule: &flag.Rule{
							VariationResult: new(variationB),
						},
						Date: new(fixedNow().Add(-1 * time.Second)),
					},
					{
						Variations: &map[string]*any{
							variationB: new(any(valueQWERTY)),
						},
						Date: new(fixedNow().Add(2 * time.Second)),
					},
				},
			},
			args: internalFlagValueArgs{
				flagName: fixtures.FlagKeyMy,
				user:     ffcontext.NewEvaluationContextBuilder(fixtures.UserKeyUser).Build(),
				flagContext: flag.Context{Service: flag.NewService(),
					DefaultSdkValue: valueDefault,
				},
			},
			want: valueBUnderscore,
			want1: flag.ResolutionDetails{
				Variant: variationB,
				Reason:  flag.ReasonStatic,
				Metadata: map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
		},
		{
			name: "Should only apply 1 scheduled step if step at the exact same time",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					variationA: new(any(valueAUnderscore)),
					variationB: new(any(valueBUnderscore)),
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(variationA),
				},
				Metadata: &map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
				Scheduled: &[]flag.ScheduledStep{
					{
						DefaultRule: &flag.Rule{
							VariationResult: new(variationB),
						},
						Date: new(time.Date(2022, 1, 1, 12, 12, 12, 12, time.UTC)),
					},
					{
						Variations: &map[string]*any{
							variationB: new(any(valueQWERTY)),
						},
						Date: new(fixedNow().Add(2 * time.Second)),
					},
				},
			},
			args: internalFlagValueArgs{
				flagName: fixtures.FlagKeyMy,
				user: ffcontext.NewEvaluationContextBuilder(fixtures.UserKeyUser).
					AddCustom(fixtures.GoFeatureFlagOrg, ffcontext.GoffContextSpecifics{
						CurrentDateTime: new(
							time.Date(2022, 1, 1, 12, 12, 12, 12, time.UTC),
						),
					}).
					Build(),
				flagContext: flag.Context{Service: flag.NewService(),
					DefaultSdkValue: valueDefault,
				},
			},
			want: valueBUnderscore,
			want1: flag.ResolutionDetails{
				Variant: variationB,
				Reason:  flag.ReasonStatic,
				Metadata: map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
		},
	})
}

func TestInternalFlag_Value_MultiScheduledOrdering(t *testing.T) {
	t.Parallel()

	runInternalFlagValueTests(t, []internalFlagValueCase{
		{
			name: "Should apply all scheduled steps in the past",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					variationA: new(any(valueAUnderscore)),
					variationB: new(any(valueBUnderscore)),
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(variationA),
				},
				Metadata: &map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
				Scheduled: &[]flag.ScheduledStep{
					{
						DefaultRule: &flag.Rule{
							VariationResult: new(variationB),
						},
						Date: new(fixedNow().Add(-2 * time.Second)),
					},
					{
						Variations: &map[string]*any{
							variationB: new(any(valueQWERTY)),
						},
						Date: new(fixedNow().Add(-1 * time.Second)),
					},
				},
			},
			args: internalFlagValueArgs{
				flagName: fixtures.FlagKeyMy,
				user:     ffcontext.NewEvaluationContextBuilder(fixtures.UserKeyUser).Build(),
				flagContext: flag.Context{Service: flag.NewService(),
					DefaultSdkValue: valueDefault,
				},
			},
			want: valueQWERTY,
			want1: flag.ResolutionDetails{
				Variant: variationB,
				Reason:  flag.ReasonStatic,
				Metadata: map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
		},
		{
			name: "Should apply the most recent scheduled step when two steps edit the same rule name (newest date wins)",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					"fixtureTrue": new(any(true)),
					fixtureFalse:  new(any(false)),
				},
				Rules: &[]flag.Rule{
					{
						Name: new(devEnvironment),
						Query: new(
							varEnvironmentUUID0Ffcc,
						),
						VariationResult: new(fixtureFalse),
					},
				},
				DefaultRule: &flag.Rule{
					Name:            new("Default"),
					VariationResult: new(fixtureFalse),
				},
				Metadata: &map[string]any{
					"type":       "ADVANCED_FLAG",
					"metadataId": "5cbd0920-7b2c-4186-9cca-3313de925fe2",
				},
				Scheduled: &[]flag.ScheduledStep{
					{ // newest date, listed first
						Rules: &[]flag.Rule{
							{
								Name: new(devEnvironment),
								Query: new(
									varEnvironmentUUID0Ffcc,
								),
								VariationResult: new(fixtureFalse),
							},
						},
						Date: new(time.Date(2026, 6, 11, 9, 50, 0, 0, time.UTC)),
					},
					{ // oldest date, listed second
						Rules: &[]flag.Rule{
							{
								Name: new(devEnvironment),
								Query: new(
									varEnvironmentUUID0Ffcc,
								),
								VariationResult: new("fixtureTrue"),
							},
						},
						Date: new(time.Date(2026, 6, 11, 9, 49, 0, 0, time.UTC)),
					},
				},
			},
			args: internalFlagValueArgs{
				flagName: fixtures.FlagKeyMy,
				user: ffcontext.NewEvaluationContextBuilder(fixtures.UserKeyUser).
					AddCustom("EnvironmentUUID", "0ffcc8da-1f70-4229-8ef7-b5a3130cdd52").
					AddCustom(fixtures.GoFeatureFlagOrg, ffcontext.GoffContextSpecifics{
						CurrentDateTime: new(
							time.Date(2026, 6, 11, 9, 51, 0, 0, time.UTC),
						),
					}).
					Build(),
				flagContext: flag.Context{Service: flag.NewService(),
					DefaultSdkValue: false,
				},
			},
			// Newest scheduled step (09:50, fixtureFalse) must win over the
			// older one (09:49, fixtureTrue).
			want: false,
			want1: flag.ResolutionDetails{
				Variant:   fixtureFalse,
				Reason:    flag.ReasonTargetingMatch,
				RuleIndex: new(0),
				RuleName:  new(devEnvironment),
				Metadata: map[string]any{
					"type":                     "ADVANCED_FLAG",
					"metadataId":               "5cbd0920-7b2c-4186-9cca-3313de925fe2",
					fixtures.EvaluatedRuleName: devEnvironment,
				},
			},
		},
		{
			name: "Should keep rules added by an earlier scheduled step when a later step adds another rule",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					variationA: new(any(valueAUnderscore)),
					variationB: new(any(valueBUnderscore)),
					variationC: new(any(valueC)),
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(variationA),
				},
				Metadata: &map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
				Scheduled: &[]flag.ScheduledStep{
					{ // older step adds rule-a
						Rules: &[]flag.Rule{
							{
								Name:            new(ruleA),
								Query:           new("key eq \"user-a\""),
								VariationResult: new(variationB),
							},
						},
						Date: new(time.Date(2026, 6, 11, 9, 49, 0, 0, time.UTC)),
					},
					{ // newer step adds a different rule-b
						Rules: &[]flag.Rule{
							{
								Name:            new("rule-b"),
								Query:           new("key eq \"user-b\""),
								VariationResult: new(variationC),
							},
						},
						Date: new(time.Date(2026, 6, 11, 9, 50, 0, 0, time.UTC)),
					},
				},
			},
			args: internalFlagValueArgs{
				flagName: fixtures.FlagKeyMy,
				user: ffcontext.NewEvaluationContextBuilder("user-a").
					AddCustom(fixtures.GoFeatureFlagOrg, ffcontext.GoffContextSpecifics{
						CurrentDateTime: new(
							time.Date(2026, 6, 11, 9, 51, 0, 0, time.UTC),
						),
					}).
					Build(),
				flagContext: flag.Context{Service: flag.NewService(),
					DefaultSdkValue: valueDefault,
				},
			},
			// rule-a (added by the older step) must survive after the newer step
			// adds rule-b, so a "user-a" context still matches rule-a.
			want: valueBUnderscore,
			want1: flag.ResolutionDetails{
				Variant:   variationB,
				Reason:    flag.ReasonTargetingMatch,
				RuleIndex: new(0),
				RuleName:  new(ruleA),
				Metadata: map[string]any{
					fixtures.Description:       thisIsAFlag,
					fixtures.IssueLink:         httpsIssueLinkGOFF,
					fixtures.EvaluatedRuleName: ruleA,
				},
			},
		},
	})
}

func TestInternalFlag_Value_ScheduledMutations(t *testing.T) {
	t.Parallel()

	runInternalFlagValueTests(t, []internalFlagValueCase{
		{
			name: "Should ignore scheduled steps with no dates",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					variationA: new(any(valueAUnderscore)),
					variationB: new(any(valueBUnderscore)),
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(variationA),
				},
				Metadata: &map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
				Scheduled: &[]flag.ScheduledStep{
					{
						Variations: &map[string]*any{
							variationA: new(any(valueQWERTY)),
						},
					},
				},
			},
			args: internalFlagValueArgs{
				flagName: fixtures.FlagKeyMy,
				user:     ffcontext.NewEvaluationContextBuilder(fixtures.UserKeyUser).Build(),
				flagContext: flag.Context{Service: flag.NewService(),
					DefaultSdkValue: valueDefault,
				},
			},
			want: valueAUnderscore,
			want1: flag.ResolutionDetails{
				Variant: variationA,
				Reason:  flag.ReasonStatic,
				Metadata: map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
		},
		{
			name: "Should update a rule that exists with a scheduled step",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					variationA: new(any(valueAUnderscore)),
					variationB: new(any(valueBUnderscore)),
				},
				Rules: &[]flag.Rule{
					{
						Name:            new(fixtures.RuleOne),
						Query:           new(keyEqUserKey),
						VariationResult: new(variationB),
					},
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(variationA),
				},
				Metadata: &map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
				Scheduled: &[]flag.ScheduledStep{
					{
						Date: new(fixedNow().Add(-1 * time.Second)),
						Variations: &map[string]*any{
							variationC: new(any(valueC)),
						},
						Rules: &[]flag.Rule{
							{
								Name:            new(fixtures.RuleOne),
								VariationResult: new(variationC),
							},
						},
					},
				},
			},
			args: internalFlagValueArgs{
				flagName: fixtures.FlagKeyMy,
				user:     ffcontext.NewEvaluationContextBuilder(fixtures.UserKeyUser).Build(),
				flagContext: flag.Context{Service: flag.NewService(),
					DefaultSdkValue: valueDefault,
				},
			},
			want: valueC,
			want1: flag.ResolutionDetails{
				Variant:   variationC,
				Reason:    flag.ReasonTargetingMatch,
				RuleIndex: new(0),
				RuleName:  new(fixtures.RuleOne),
				Metadata: map[string]any{
					fixtures.Description:       thisIsAFlag,
					fixtures.IssueLink:         httpsIssueLinkGOFF,
					fixtures.EvaluatedRuleName: fixtures.RuleOne,
				},
			},
		},
		{
			name: "Should update default rule with a scheduled step",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					variationA: new(any(valueAUnderscore)),
					variationB: new(any(valueBUnderscore)),
					variationC: new(any(valueC)),
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(variationC),
				},
				Metadata: &map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
				Scheduled: &[]flag.ScheduledStep{
					{
						Date: new(fixedNow().Add(-2 * time.Second)),
						DefaultRule: &flag.Rule{
							Percentages: &map[string]float64{
								variationB: 30,
								variationC: 70,
							},
						},
					},
				},
			},
			args: internalFlagValueArgs{
				flagName: fixtures.FlagKeyMy,
				user:     ffcontext.NewEvaluationContextBuilder("user-key-123").Build(),
				flagContext: flag.Context{Service: flag.NewService(),
					DefaultSdkValue: valueDefault,
				},
			},
			want: valueC,
			want1: flag.ResolutionDetails{
				Variant: variationC,
				Reason:  flag.ReasonSplit,
				Metadata: map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
		},
		{
			name: "Should add a new rule with a scheduled step",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					variationA: new(any(valueAUnderscore)),
					variationB: new(any(valueBUnderscore)),
				},
				Rules: &[]flag.Rule{
					{
						Name:            new(fixtures.RuleOne),
						Query:           new(keyEqUserKey),
						VariationResult: new(variationB),
					},
				},
				Metadata: &map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(variationA),
				},
				Scheduled: &[]flag.ScheduledStep{
					{
						Date: new(fixedNow().Add(-1 * time.Second)),
						Variations: &map[string]*any{
							variationC: new(any(valueC)),
						},
						Rules: &[]flag.Rule{
							{
								Name:            new(fixtures.RuleTwo),
								Query:           new("key eq \"user-key-123\""),
								VariationResult: new(variationC),
							},
						},
					},
				},
			},
			args: internalFlagValueArgs{
				flagName: fixtures.FlagKeyMy,
				user:     ffcontext.NewEvaluationContextBuilder("user-key-123").Build(),
				flagContext: flag.Context{Service: flag.NewService(),
					DefaultSdkValue: valueDefault,
				},
			},
			want: valueC,
			want1: flag.ResolutionDetails{
				Variant:   variationC,
				Reason:    flag.ReasonTargetingMatch,
				RuleIndex: new(1),
				RuleName:  new(fixtures.RuleTwo),
				Metadata: map[string]any{
					fixtures.Description:       thisIsAFlag,
					fixtures.IssueLink:         httpsIssueLinkGOFF,
					fixtures.EvaluatedRuleName: fixtures.RuleTwo,
				},
			},
		},
	})
}

func TestInternalFlag_Value_ScheduledRolloutsAndExperimentation(t *testing.T) {
	t.Parallel()

	runInternalFlagValueTests(t, []internalFlagValueCase{
		{
			name: "Should apply all the changes if all scheduled steps are in the past",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					variationA: new(any(valueAUnderscore)),
					variationB: new(any(valueBUnderscore)),
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(variationA),
				},
				Metadata: &map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
				Scheduled: &[]flag.ScheduledStep{
					{
						DefaultRule: &flag.Rule{
							VariationResult: new(variationB),
						},
						Date: new(fixedNow().Add(-2 * time.Second)),
					},
					{
						Variations: &map[string]*any{
							variationB: new(any(valueQWERTY)),
						},
						Date: new(fixedNow().Add(-1 * time.Second)),
					},
				},
			},
			args: internalFlagValueArgs{
				flagName: fixtures.FlagKeyMy,
				user:     ffcontext.NewEvaluationContextBuilder(fixtures.UserKeyUser).Build(),
				flagContext: flag.Context{Service: flag.NewService(),
					DefaultSdkValue: valueDefault,
				},
			},
			want: valueQWERTY,
			want1: flag.ResolutionDetails{
				Variant: variationB,
				Reason:  flag.ReasonStatic,
				Metadata: map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
		},
		{
			name: "Should disable the flag with a scheduled step",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					variationA: new(any(valueAUnderscore)),
					variationB: new(any(valueBUnderscore)),
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(variationA),
				},
				Metadata: &map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
				Scheduled: &[]flag.ScheduledStep{
					{
						Disable:     new(true),
						TrackEvents: new(false),
						Version:     new(fixtures.VersionOneZeroZero),
						Date:        new(fixedNow().Add(-2 * time.Second)),
					},
				},
			},
			args: internalFlagValueArgs{
				flagName: fixtures.FlagKeyMy,
				user:     ffcontext.NewEvaluationContextBuilder(fixtures.UserKeyUser).Build(),
				flagContext: flag.Context{Service: flag.NewService(),
					DefaultSdkValue: valueDefault,
				},
			},
			want: valueDefault,
			want1: flag.ResolutionDetails{
				Variant: flag.VariationSDKDefault,
				Reason:  flag.ReasonDisabled,
				Metadata: map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
		},
		{
			name: "Should create an experimentation for a dedicated time",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					variationA: new(any(valueAUnderscore)),
					variationB: new(any(valueBUnderscore)),
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(variationA),
				},
				Metadata: &map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
				Scheduled: &[]flag.ScheduledStep{
					{
						Experimentation: &flag.ExperimentationRollout{
							Start: new(fixedNow().Add(-2 * time.Second)),
							End:   new(fixedNow().Add(2 * time.Second)),
						},
						Date: new(fixedNow().Add(-1 * time.Second)),
					},
				},
			},
			args: internalFlagValueArgs{
				flagName: fixtures.FlagKeyMy,
				user:     ffcontext.NewEvaluationContextBuilder(fixtures.UserKeyUser).Build(),
				flagContext: flag.Context{Service: flag.NewService(),
					DefaultSdkValue: valueDefault,
				},
			},
			want: valueAUnderscore,
			want1: flag.ResolutionDetails{
				Variant: variationA,
				Reason:  flag.ReasonStatic,
				Metadata: map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
		},
		{
			name: "Should not apply a scheduled step inside another scheduled step",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					variationA: new(any(valueAUnderscore)),
					variationB: new(any(valueBUnderscore)),
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(variationA),
				},
				Metadata: &map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
				Scheduled: &[]flag.ScheduledStep{
					{
						Variations: &map[string]*any{
							variationA: new(any("value_AB")),
							variationB: new(any(valueBUnderscore)),
						},
						Scheduled: &[]flag.ScheduledStep{
							{
								Variations: &map[string]*any{
									variationA: new(any("value_ABC")),
								},
								Date: new(fixedNow().Add(-3 * time.Second)),
							},
						},
						Date: new(fixedNow().Add(-2 * time.Second)),
					},
				},
			},
			args: internalFlagValueArgs{
				flagName: fixtures.FlagKeyMy,
				user:     ffcontext.NewEvaluationContextBuilder(fixtures.UserKeyUser).Build(),
				flagContext: flag.Context{Service: flag.NewService(),
					DefaultSdkValue: valueDefault,
				},
			},
			want: "value_AB",
			want1: flag.ResolutionDetails{
				Variant: variationA,
				Reason:  flag.ReasonStatic,
				Metadata: map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
		},
	})
}

func TestInternalFlag_Value_RolloutAndTargetingReasons(t *testing.T) {
	t.Parallel()

	runInternalFlagValueTests(t, []internalFlagValueCase{
		{
			name: "Should return the false value if not in between initial and end percentage",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					variationA: new(any(valueAUnderscore)),
					variationB: new(any(valueBUnderscore)),
				},
				DefaultRule: &flag.Rule{
					ProgressiveRollout: &flag.ProgressiveRollout{
						Initial: &flag.ProgressiveRolloutStep{
							Variation:  new(variationA),
							Percentage: new(float64(0)),
							Date:       new(fixedNow().Add(-10 * time.Second)),
						},
						End: &flag.ProgressiveRolloutStep{
							Variation:  new(variationB),
							Percentage: new(float64(5)),
							Date:       new(fixedNow().Add(-1 * time.Second)),
						},
					},
				},
				Metadata: &map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
			args: internalFlagValueArgs{
				flagName: fixtures.FlagKeyMy,
				user:     ffcontext.NewEvaluationContextBuilder(fixtures.UserKeyUser).Build(),
				flagContext: flag.Context{Service: flag.NewService(),
					DefaultSdkValue: valueDefault,
				},
			},
			want: valueAUnderscore,
			want1: flag.ResolutionDetails{
				Variant:   variationA,
				Reason:    flag.ReasonSplit,
				Cacheable: false,
				Metadata: map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
		},
		{
			name: "Should return the reason TARGETING_MATCH if rule apply and return a simple variation",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					variationA: new(any(valueAUnderscore)),
					variationB: new(any(valueBUnderscore)),
				},
				Rules: &[]flag.Rule{
					{
						Name:            new(testRule),
						Query:           new(keyEqUserKey),
						VariationResult: new(variationA),
					},
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(variationA),
				},
				Metadata: &map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
			args: internalFlagValueArgs{
				flagName: fixtures.FlagKeyMy,
				user:     ffcontext.NewEvaluationContextBuilder(fixtures.UserKeyUser).Build(),
				flagContext: flag.Context{Service: flag.NewService(),
					DefaultSdkValue: valueDefault,
				},
			},
			want: valueAUnderscore,
			want1: flag.ResolutionDetails{
				Variant:   variationA,
				Reason:    flag.ReasonTargetingMatch,
				RuleIndex: new(0),
				RuleName:  new(testRule),
				Cacheable: true,
				Metadata: map[string]any{
					fixtures.Description:       thisIsAFlag,
					fixtures.IssueLink:         httpsIssueLinkGOFF,
					fixtures.EvaluatedRuleName: testRule,
				},
			},
		},
		{
			name: "Should return the reason TARGETING_MATCH_SPLIT if rule apply and has percentage",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					variationA: new(any(valueAUnderscore)),
					variationB: new(any(valueBUnderscore)),
				},
				Rules: &[]flag.Rule{
					{
						Name:  new(testRule),
						Query: new(keyEqUserKey),
						Percentages: &map[string]float64{
							variationA: 50,
							variationB: 50,
						},
					},
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(variationA),
				},
				Metadata: &map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
			args: internalFlagValueArgs{
				flagName: fixtures.FlagKeyMy,
				user:     ffcontext.NewEvaluationContextBuilder(fixtures.UserKeyUser).Build(),
				flagContext: flag.Context{Service: flag.NewService(),
					DefaultSdkValue: valueDefault,
				},
			},
			want: valueAUnderscore,
			want1: flag.ResolutionDetails{
				Variant:   variationA,
				Reason:    flag.ReasonTargetingMatchSplit,
				RuleIndex: new(0),
				RuleName:  new(testRule),
				Cacheable: true,
				Metadata: map[string]any{
					fixtures.Description:       thisIsAFlag,
					fixtures.IssueLink:         httpsIssueLinkGOFF,
					fixtures.EvaluatedRuleName: testRule,
				},
			},
		},
		{
			name: "Should return the reason TARGETING_MATCH_SPLIT if rule apply and has progressive rollout",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					variationA: new(any(valueAUnderscore)),
					variationB: new(any(valueBUnderscore)),
				},
				Rules: &[]flag.Rule{
					{
						Name:  new(testRule),
						Query: new(keyEqUserKey),
						ProgressiveRollout: &flag.ProgressiveRollout{
							Initial: &flag.ProgressiveRolloutStep{
								Variation:  new(variationA),
								Percentage: new(float64(0)),
								Date:       new(fixedNow()),
							},
							End: &flag.ProgressiveRolloutStep{
								Variation:  new(variationB),
								Percentage: new(float64(100)),
								Date:       new(fixedNow().Add(1 * time.Minute)),
							},
						},
					},
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(variationA),
				},
				Metadata: &map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
			args: internalFlagValueArgs{
				flagName: fixtures.FlagKeyMy,
				user:     ffcontext.NewEvaluationContextBuilder(fixtures.UserKeyUser).Build(),
				flagContext: flag.Context{Service: flag.NewService(),
					DefaultSdkValue: valueDefault,
				},
			},
			want: valueAUnderscore,
			want1: flag.ResolutionDetails{
				Variant:   variationA,
				Reason:    flag.ReasonTargetingMatchSplit,
				RuleIndex: new(0),
				RuleName:  new(testRule),
				Cacheable: false,
				Metadata: map[string]any{
					fixtures.Description:       thisIsAFlag,
					fixtures.IssueLink:         httpsIssueLinkGOFF,
					fixtures.EvaluatedRuleName: testRule,
				},
			},
		},
	})
}

func TestInternalFlag_Value_DefaultAndStaticReasons(t *testing.T) {
	t.Parallel()

	runInternalFlagValueTests(t, []internalFlagValueCase{
		{
			name: "Should return the reason SPLIT if rule not apply and has percentage",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					variationA: new(any(valueAUnderscore)),
					variationB: new(any(valueBUnderscore)),
				},
				Rules: &[]flag.Rule{
					{
						Name:            new(testRule),
						Query:           new("key eq \"user-key2\""),
						VariationResult: new(variationB),
					},
				},
				DefaultRule: &flag.Rule{
					Percentages: &map[string]float64{
						variationA: 50,
						variationB: 50,
					},
				},
				Metadata: &map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
			args: internalFlagValueArgs{
				flagName: fixtures.FlagKeyMy,
				user:     ffcontext.NewEvaluationContextBuilder(fixtures.UserKeyUser).Build(),
				flagContext: flag.Context{Service: flag.NewService(),
					DefaultSdkValue: valueDefault,
				},
			},
			want: valueAUnderscore,
			want1: flag.ResolutionDetails{
				Variant:   variationA,
				Reason:    flag.ReasonSplit,
				Cacheable: true,
				Metadata: map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
		},
		{
			name: "Should return the reason DEFAULT if rule not apply and has default variation",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					variationA: new(any(valueAUnderscore)),
					variationB: new(any(valueBUnderscore)),
				},
				Rules: &[]flag.Rule{
					{
						Name:            new(testRule),
						Query:           new("key eq \"user-key2\""),
						VariationResult: new(variationB),
					},
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(variationA),
				},
				Metadata: &map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
			args: internalFlagValueArgs{
				flagName: fixtures.FlagKeyMy,
				user:     ffcontext.NewEvaluationContextBuilder(fixtures.UserKeyUser).Build(),
				flagContext: flag.Context{Service: flag.NewService(),
					DefaultSdkValue: valueDefault,
				},
			},
			want: valueAUnderscore,
			want1: flag.ResolutionDetails{
				Variant:   variationA,
				Reason:    flag.ReasonDefault,
				Cacheable: true,
				Metadata: map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
		},
		{
			name: "Should return the reason STATIC if no rule and has default variation",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					variationA: new(any(valueAUnderscore)),
					variationB: new(any(valueBUnderscore)),
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(variationA),
				},
				Metadata: &map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
			args: internalFlagValueArgs{
				flagName: fixtures.FlagKeyMy,
				user:     ffcontext.NewEvaluationContextBuilder(fixtures.UserKeyUser).Build(),
				flagContext: flag.Context{Service: flag.NewService(),
					DefaultSdkValue: valueDefault,
				},
			},
			want: valueAUnderscore,
			want1: flag.ResolutionDetails{
				Variant:   variationA,
				Reason:    flag.ReasonStatic,
				Cacheable: true,
				Metadata: map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
		},
		{
			name: "Should return the reason STATIC if no rule and has default percentage to 100%",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					variationA: new(any(valueAUnderscore)),
					variationB: new(any(valueBUnderscore)),
				},
				DefaultRule: &flag.Rule{
					Percentages: &map[string]float64{
						variationA: 100,
						variationB: 0,
					},
				},
				Metadata: &map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
			args: internalFlagValueArgs{
				flagName: fixtures.FlagKeyMy,
				user:     ffcontext.NewEvaluationContextBuilder(fixtures.UserKeyUser).Build(),
				flagContext: flag.Context{Service: flag.NewService(),
					DefaultSdkValue: valueDefault,
				},
			},
			want: valueAUnderscore,
			want1: flag.ResolutionDetails{
				Variant:   variationA,
				Reason:    flag.ReasonStatic,
				Cacheable: true,
				Metadata: map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
		},
		{
			name: "Should use the environment as a rule criteria",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					"A": new(any(false)),
					"B": new(any(true)),
				},
				Rules: &[]flag.Rule{
					{
						Query:           new("env == \"development\""),
						VariationResult: new("A"),
					},
					{
						Query: new("(env == \"production\") " +
							"or (env == \"staging\") or (env == \"qa\")"),
						VariationResult: new("B"),
					},
				},
				DefaultRule: &flag.Rule{
					VariationResult: new("B"),
				},
				Metadata: &map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
			args: internalFlagValueArgs{
				flagName: "feature",
				user:     ffcontext.NewEvaluationContextBuilder("1").Build(),
				flagContext: flag.Context{Service: flag.NewService(),
					DefaultSdkValue: true,
					EvaluationContextEnrichment: map[string]any{
						fixtures.Env: "development",
					},
				},
			},
			want: false,
			want1: flag.ResolutionDetails{
				Variant:   "A",
				Reason:    flag.ReasonTargetingMatch,
				Cacheable: true,
				RuleIndex: new(0),
				Metadata: map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
		},
	})
}

func TestInternalFlag_Value_EnrichmentAndMissingKeys(t *testing.T) {
	t.Parallel()

	runInternalFlagValueTests(t, []internalFlagValueCase{
		{
			name: "Should have a targeting match if common evaluation context match",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					"A": new(any("A")),
					"B": new(any("B")),
				},
				Rules: &[]flag.Rule{
					{
						Query:           new("environment eq \"prod\""),
						VariationResult: new("A"),
					},
				},
				DefaultRule: &flag.Rule{
					VariationResult: new("B"),
				},
			},
			args: internalFlagValueArgs{
				flagName: fixtures.FlagKeyMy,
				user:     ffcontext.NewEvaluationContext(fixtures.UserKeyUser),
				flagContext: flag.Context{Service: flag.NewService(),
					DefaultSdkValue: defaultSdk,
					EvaluationContextEnrichment: map[string]any{
						environmentKey: fixtures.Prod,
					},
				},
			},
			want: "A",
			want1: flag.ResolutionDetails{
				Variant:   "A",
				Reason:    flag.ReasonTargetingMatch,
				RuleIndex: new(0),
				Cacheable: true,
			},
		},
		{
			name: "EvaluationContextEnrichment should override request evaluation context",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					"A": new(any("A")),
					"B": new(any("B")),
				},
				Rules: &[]flag.Rule{
					{
						Query:           new("environment eq \"prod\""),
						VariationResult: new("A"),
					},
				},
				DefaultRule: &flag.Rule{
					VariationResult: new("B"),
				},
			},
			args: internalFlagValueArgs{
				flagName: fixtures.FlagKeyMy,
				user: ffcontext.NewEvaluationContextBuilder("key1").
					AddCustom(environmentKey, "dev").
					Build(),
				flagContext: flag.Context{Service: flag.NewService(),
					DefaultSdkValue: defaultSdk,
					EvaluationContextEnrichment: map[string]any{
						environmentKey: fixtures.Prod,
					},
				},
			},
			want: "A",
			want1: flag.ResolutionDetails{
				Variant:   "A",
				Reason:    flag.ReasonTargetingMatch,
				RuleIndex: new(0),
				Cacheable: true,
			},
		},
		{
			name: "Should return sdk default value when we have an error in the deep copy",
			flag: flag.InternalFlag{
				Experimentation: &flag.ExperimentationRollout{
					Start: new(fixedNow().Add(-15 * time.Second)),
					End:   new(fixedNow().Add(-5 * time.Second)),
				},
				Metadata: &map[string]any{
					fixtures.Description: make(chan int),
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
				Scheduled: &[]flag.ScheduledStep{
					{
						Date: new(fixedNow().Add(-10 * time.Second)),
						Experimentation: &flag.ExperimentationRollout{
							Start: new(fixedNow().Add(-5 * time.Second)),
							End:   new(fixedNow().Add(5 * time.Second)),
						},
					},
				},
			},
			args: internalFlagValueArgs{
				flagName: fixtures.FlagKeyMy,
				user:     ffcontext.NewEvaluationContext(fixtures.UserKeyUser),
				flagContext: flag.Context{Service: flag.NewService(),
					DefaultSdkValue: defaultSdk,
				},
			},
			want: defaultSdk,
			want1: flag.ResolutionDetails{
				Variant:      sdkDefault,
				Reason:       flag.ReasonError,
				ErrorCode:    flag.ErrorCodeGeneral,
				ErrorMessage: "encoding the flag for the deep copy: json: unsupported type: chan int",
			},
		},
		{
			name: "Empty targeting key for static flag should succeed",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					variationA: new(any(true)),
					variationB: new(any(false)),
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(variationA),
				},
				Metadata: &map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
			args: internalFlagValueArgs{
				flagName: fixtures.FlagKeyMy,
				user:     ffcontext.NewEvaluationContext(""),
				flagContext: flag.Context{Service: flag.NewService(),
					DefaultSdkValue: false,
				},
			},
			want: true,
			want1: flag.ResolutionDetails{
				Variant:   variationA,
				Reason:    flag.ReasonStatic,
				Cacheable: true,
				Metadata: map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
		},
		{
			name: "Empty bucketing key for static flag should succeed",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					variationA: new(any(true)),
					variationB: new(any(false)),
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(variationA),
				},
				Metadata: &map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
				BucketingKey: new(teamID),
			},
			args: internalFlagValueArgs{
				flagName: fixtures.FlagKeyMy,
				user: ffcontext.NewEvaluationContextBuilder("toto").
					AddCustom(teamID, "").
					Build(),
				flagContext: flag.Context{Service: flag.NewService(),
					DefaultSdkValue: false,
				},
			},
			want: true,
			want1: flag.ResolutionDetails{
				Variant:   variationA,
				Reason:    flag.ReasonStatic,
				Cacheable: true,
				Metadata: map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
		},
		{
			name: "Empty targeting key for percentage flag should fail",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					variationA: new(any(true)),
					variationB: new(any(false)),
				},
				DefaultRule: &flag.Rule{
					Percentages: &map[string]float64{
						variationA: 50,
						variationB: 50,
					},
				},
				Metadata: &map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
			args: internalFlagValueArgs{
				flagName: fixtures.FlagKeyMy,
				user:     ffcontext.NewEvaluationContext(""),
				flagContext: flag.Context{Service: flag.NewService(),
					DefaultSdkValue: false,
				},
			},
			want: false,
			want1: flag.ResolutionDetails{
				Variant:      sdkDefault,
				Reason:       flag.ReasonError,
				ErrorCode:    flag.ErrorCodeTargetingKeyMissing,
				ErrorMessage: "Error: Empty targeting key",
				Cacheable:    false,
				Metadata: map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
		},
	})
}

func TestInternalFlag_ValueWithBucketingKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                string
		flag                flag.InternalFlag
		args                internalFlagValueArgs
		wantForTargetingKey string
		wantForTeamID       string
	}{
		{
			name: "Should use custom bucketing key when set",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					variationA: new(any(valueAUnderscore)),
					variationB: new(any(valueBUnderscore)),
					variationC: new(any(valueC)),
				},
				BucketingKey: new(teamID),
				DefaultRule: &flag.Rule{
					Percentages: &map[string]float64{
						variationA: 33,
						variationB: 67,
					},
				},
			},
			args: internalFlagValueArgs{
				flagName: fixtures.FlagKeyMy,
				user: ffcontext.NewEvaluationContextBuilder(fixtures.UserKeyUser).
					AddCustom(teamID, team123).
					Build(),
				flagContext: flag.Context{Service: flag.NewService(),
					DefaultSdkValue: valueDefault,
				},
			},
			wantForTargetingKey: variationA,
			wantForTeamID:       variationB,
		},
		{
			name: "Should use nested bucketing key when set",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					variationA: new(any(valueAUnderscore)),
					variationB: new(any(valueBUnderscore)),
					variationC: new(any(valueC)),
				},
				BucketingKey: new(companyID),
				DefaultRule: &flag.Rule{
					Percentages: &map[string]float64{
						variationA: 33,
						variationB: 67,
					},
				},
			},
			args: internalFlagValueArgs{
				flagName: fixtures.FlagKeyMy,
				user: ffcontext.NewEvaluationContextBuilder(fixtures.UserKeyUser).
					AddCustom(teamID, team123).
					AddCustom(fixtures.UserCompany, map[string]any{
						"id": "company-456",
					}).
					Build(),
				flagContext: flag.Context{Service: flag.NewService(),
					DefaultSdkValue: valueDefault,
				},
			},
			wantForTargetingKey: variationA,
			wantForTeamID:       variationB,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			flagWithBucketingKey := tt.flag

			_, got := flagWithBucketingKey.Value(
				tt.args.flagName,
				tt.args.user,
				tt.args.flagContext,
			)
			assert.Equal(t, tt.wantForTeamID, got.Variant)

			flagWithoutBucketingKey := tt.flag
			flagWithoutBucketingKey.BucketingKey = new("")
			_, got = flagWithoutBucketingKey.Value(
				tt.args.flagName,
				tt.args.user,
				tt.args.flagContext,
			)

			assert.Equal(t, tt.wantForTargetingKey, got.Variant)
		})
	}
}

func TestInternalFlag_ValueWithInvalidBucketingKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		flag flag.InternalFlag
		args internalFlagValueArgs
		name string
		want flag.ResolutionDetails
	}{
		{
			name: "Should return error when bucketing key not found",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					variationA: new(any(valueAUnderscore)),
					variationB: new(any(valueBUnderscore)),
					variationC: new(any(valueC)),
				},
				BucketingKey: new(teamID),
				DefaultRule: &flag.Rule{
					Percentages: &map[string]float64{
						variationA: 33,
						variationB: 67,
					},
				},
			},
			args: internalFlagValueArgs{
				flagName:    fixtures.FlagKeyMy,
				user:        ffcontext.NewEvaluationContextBuilder(fixtures.UserKeyUser).Build(),
				flagContext: flag.Context{Service: flag.NewService()},
			},
			want: flag.ResolutionDetails{
				Variant:      sdkDefault,
				Reason:       flag.ReasonError,
				ErrorCode:    flag.ErrorCodeTargetingKeyMissing,
				ErrorMessage: "impossible to find bucketingKey in context: nested key not found: teamId",
			},
		},
		{
			name: "Should return error when nested bucketing key not found",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					variationA: new(any(valueAUnderscore)),
					variationB: new(any(valueBUnderscore)),
					variationC: new(any(valueC)),
				},
				BucketingKey: new(companyID),
				DefaultRule: &flag.Rule{
					Percentages: &map[string]float64{
						variationA: 33,
						variationB: 67,
					},
				},
			},
			args: internalFlagValueArgs{
				flagName: fixtures.FlagKeyMy,
				user: ffcontext.NewEvaluationContextBuilder(fixtures.UserKeyUser).
					AddCustom(fixtures.UserCompany, map[string]any{
						"name": "acme-corp",
					}).
					Build(),
				flagContext: flag.Context{Service: flag.NewService()},
			},
			want: flag.ResolutionDetails{
				Variant:      sdkDefault,
				Reason:       flag.ReasonError,
				ErrorCode:    flag.ErrorCodeTargetingKeyMissing,
				ErrorMessage: "impossible to find bucketingKey in context: nested key not found: company.id",
			},
		},
		{
			name: "Should return error when nested bucketing key is not a string",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					variationA: new(any(valueAUnderscore)),
					variationB: new(any(valueBUnderscore)),
					variationC: new(any(valueC)),
				},
				BucketingKey: new(companyID),
				DefaultRule: &flag.Rule{
					Percentages: &map[string]float64{
						variationA: 33,
						variationB: 67,
					},
				},
			},
			args: internalFlagValueArgs{
				flagName: fixtures.FlagKeyMy,
				user: ffcontext.NewEvaluationContextBuilder(fixtures.UserKeyUser).
					AddCustom(fixtures.UserCompany, map[string]any{
						"id": 12345,
					}).
					Build(),
				flagContext: flag.Context{Service: flag.NewService()},
			},
			want: flag.ResolutionDetails{
				Variant:      sdkDefault,
				Reason:       flag.ReasonError,
				ErrorCode:    flag.ErrorCodeTargetingKeyMissing,
				ErrorMessage: "invalid bucketing key",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, got := tt.flag.Value(tt.args.flagName, tt.args.user, tt.args.flagContext)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFlag_ProgressiveRollout(t *testing.T) {
	t.Parallel()

	flg := &flag.InternalFlag{
		Variations: &map[string]*any{
			variationA: new(any(valueAUnderscore)),
			variationB: new(any(valueBUnderscore)),
		},
		DefaultRule: &flag.Rule{
			ProgressiveRollout: &flag.ProgressiveRollout{
				Initial: &flag.ProgressiveRolloutStep{
					Variation:  new(variationA),
					Percentage: new(float64(0)),
					Date:       new(clocktest.Instant().Add(1 * time.Second)),
				},
				End: &flag.ProgressiveRolloutStep{
					Variation:  new(variationB),
					Percentage: new(float64(100)),
					Date:       new(clocktest.Instant().Add(2 * time.Second)),
				},
			},
		},
	}

	user := ffcontext.NewEvaluationContextBuilder("test").AddCustom(fixtures.Anonymous, true).Build()
	flagName := fixtures.FlagKeyTest

	// We evaluate the same flag at different dates: before the initial step, at the initial step,
	// and at the end of the progressive rollout.
	tests := []struct {
		name string
		date time.Time
		want string
	}{
		{name: "before the rollout starts", date: clocktest.Instant(), want: variationA},
		{name: "at the first step", date: clocktest.Instant().Add(1 * time.Second), want: variationA},
		{name: "at the end of the rollout", date: clocktest.Instant().Add(2 * time.Second), want: variationB},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			v, _ := flg.Value(flagName, atDate(user, tt.date), flag.Context{Service: flag.NewService()})
			assert.Equal(t, flg.GetVariationValue(tt.want), v)
		})
	}
}

func TestInternalFlag_GetVariations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		flag flag.InternalFlag
		want map[string]*any
		name string
	}{
		{
			name: "Should return empty map if variations nil",
			flag: flag.InternalFlag{Variations: nil},
			want: map[string]*any{},
		},
		{
			name: "Should return empty map if variations empty map",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{},
			},
			want: map[string]*any{},
		},
		{
			name: "Should return variations if map is not empty",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					varAName: new(any(valueA)),
					varBName: new(any(valueB)),
				},
			},
			want: map[string]*any{
				varAName: new(any(valueA)),
				varBName: new(any(valueB)),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equalf(t, tt.want, tt.flag.GetVariations(), "GetVariations()")
		})
	}
}

func TestInternalFlag_GetRuleIndexByName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		flag     flag.InternalFlag
		want     *int
		name     string
		ruleName string
	}{
		{
			name: "Should return nil if no rules in flag",
			flag: flag.InternalFlag{
				Rules: nil,
			},
			ruleName: fixtures.RuleOne,
			want:     nil,
		},
		{
			name: "Should return nil if empty slide of rule",
			flag: flag.InternalFlag{
				Rules: &[]flag.Rule{},
			},
			ruleName: fixtures.RuleOne,
			want:     nil,
		},
		{
			name: "Should return nil if empty slide of rule",
			flag: flag.InternalFlag{
				Rules: &[]flag.Rule{
					{
						Name: new("rule0"),
					},
					{
						Name: new(fixtures.RuleOne),
					},
				},
			},
			ruleName: fixtures.RuleOne,
			want:     new(1),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equalf(
				t,
				tt.want,
				tt.flag.GetRuleIndexByName(tt.ruleName),
				"GetRuleIndexByName(%v)",
				tt.ruleName,
			)
		})
	}
}

func TestInternalFlag_GetVariationValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		flag      flag.InternalFlag
		want      any
		name      string
		variation string
	}{
		{
			name: "Should return nil if variation does not exist",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					varAName: new(any(valueA)),
					varBName: new(any(valueB)),
				},
			},
			variation: "varC",
			want:      nil,
		},
		{
			name: "Should return variation value if exists",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					varAName: new(any(valueA)),
					varBName: new(any(valueB)),
				},
			},
			variation: varAName,
			want:      valueA,
		},
		{
			name: "Should return nil if variation value is nil",
			flag: flag.InternalFlag{
				Variations: &map[string]*any{
					varAName: new(any(nil)),
					varBName: new(any(valueB)),
				},
			},
			variation: varAName,
			want:      nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equalf(
				t,
				tt.want,
				tt.flag.GetVariationValue(tt.variation),
				"GetVariationValue(%v)",
				tt.variation,
			)
		})
	}
}

func TestInternalFlag_IsValid(t *testing.T) {
	t.Parallel()

	runInternalFlagValidTests(t, []internalFlagValidCase{
		{
			name: "no variation",
			fields: internalFlagValidFields{
				Variations: &map[string]*any{},
			},
			wantErr:  assert.Error,
			errorMsg: "no variation available",
		},
		{
			name: "different types in variation",
			fields: internalFlagValidFields{
				Variations: &map[string]*any{
					"A": new(any("A")),
					"C": new(any("C")),
					"B": new(any(120.1)),
				},
				DefaultRule: &flag.Rule{
					VariationResult: new("A"),
				},
				Metadata: &map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
			errorMsg: "all variations should have the same type",
			wantErr:  assert.Error,
		},
		{
			name: "different types in variation int/float should be ok",
			fields: internalFlagValidFields{
				Variations: &map[string]*any{
					"A": new(any(120)),
					"B": new(any(120.1)),
				},
				DefaultRule: &flag.Rule{
					VariationResult: new("A"),
				},
				Metadata: &map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
			wantErr: assert.NoError,
		},
		{
			name: "no default rule",
			fields: internalFlagValidFields{
				Variations: &map[string]*any{
					"A": new(any("A")),
				},
				Rules: &[]flag.Rule{
					{
						Name:            new(ruleOne),
						Query:           new("key eq 1"),
						VariationResult: new("A"),
					},
				},
				Metadata: &map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
			errorMsg: "missing default rule",
			wantErr:  assert.Error,
		},
		{
			name: "multiple rule with same name",
			fields: internalFlagValidFields{
				Variations: &map[string]*any{
					"A": new(any("A")),
				},
				DefaultRule: &flag.Rule{
					VariationResult: new("A"),
				},
				Metadata: &map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
				Rules: &[]flag.Rule{
					{
						Name:            new(ruleOne),
						Query:           new("key eq 1"),
						VariationResult: new("A"),
					},
					{
						Name:            new(""),
						Query:           new("key eq 3"),
						VariationResult: new("A"),
					},
					{
						Name:            new("Rule2"),
						Query:           new("key eq 2"),
						VariationResult: new("A"),
					},
					{
						Name:            new(""),
						Query:           new("key eq 3"),
						VariationResult: new("A"),
					},
					{
						Name:            new(ruleOne),
						Query:           new("key eq 4"),
						VariationResult: new("A"),
					},
					{
						Name:            new(""),
						Query:           new(keyEqFive),
						VariationResult: new("A"),
					},
				},
			},
			errorMsg: "duplicated rule name: Rule1",
			wantErr:  assert.Error,
		},
		{
			name: "wrong percentages for default rule",
			fields: internalFlagValidFields{
				Variations: &map[string]*any{
					"A": new(any("A")),
					"B": new(any("B")),
				},
				DefaultRule: &flag.Rule{
					Percentages: &map[string]float64{
						"A": 0o0,
						"B": 0,
					},
				},
				Metadata: &map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
			errorMsg: invalidPercentagesShouldNotBe,
			wantErr:  assert.Error,
		},
		{
			name: "empty percentages for default rule",
			fields: internalFlagValidFields{
				Variations: &map[string]*any{
					"A": new(any("A")),
					"B": new(any("B")),
				},
				DefaultRule: &flag.Rule{
					Percentages: &map[string]float64{},
				},
				Metadata: &map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
			errorMsg: invalidPercentagesShouldNot,
			wantErr:  assert.Error,
		},
	})
}

func TestInternalFlag_IsValid_TargetingRules(t *testing.T) {
	t.Parallel()

	runInternalFlagValidTests(t, []internalFlagValidCase{
		{
			name: "wrong percentages for targeting",
			fields: internalFlagValidFields{
				Variations: &map[string]*any{
					"A": new(any("A")),
					"B": new(any("B")),
				},
				DefaultRule: &flag.Rule{
					Percentages: &map[string]float64{
						"A": 0o0,
						"B": 0o0,
					},
				},
				Metadata: &map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
				Rules: &[]flag.Rule{
					{
						Name:  new(ruleOne),
						Query: new(keyEqFive),
						Percentages: &map[string]float64{
							"A": 0o0,
							"B": 0,
						},
					},
				},
			},
			errorMsg: invalidPercentagesShouldNotBe,
			wantErr:  assert.Error,
		},
		{
			name: "empty percentages for targeting",
			fields: internalFlagValidFields{
				Variations: &map[string]*any{
					"A": new(any("A")),
				},
				DefaultRule: &flag.Rule{
					Percentages: &map[string]float64{},
				},
				Metadata: &map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
				Rules: &[]flag.Rule{
					{
						Name:        new(ruleOne),
						Query:       new(keyEqFive),
						Percentages: &map[string]float64{},
					},
				},
			},
			errorMsg: invalidPercentagesShouldNot,
			wantErr:  assert.Error,
		},
		{
			name: "targeting without query",
			fields: internalFlagValidFields{
				Variations: &map[string]*any{
					"A": new(any("A")),
					"B": new(any("B")),
				},
				DefaultRule: &flag.Rule{
					Percentages: &map[string]float64{
						"A": 90,
						"B": 10,
					},
				},
				Rules: &[]flag.Rule{
					{
						Name: new(ruleOne),
						Percentages: &map[string]float64{
							"A": 90,
							"B": 10,
						},
					},
				},
				Metadata: &map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			},
			errorMsg: "each targeting should have a query",
			wantErr:  assert.Error,
		},
		{
			name: "Nothing to return in rule",
			fields: internalFlagValidFields{
				Variations: &map[string]*any{
					"A": new(any("A")),
					"B": new(any("B")),
				},
				Metadata: &map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
				DefaultRule: &flag.Rule{
					Name: new(nothingToReturn),
				},
			},
			errorMsg: "impossible to return value",
			wantErr:  assert.Error,
		},
		{
			name: "progressive rollout percentage initial > end",
			fields: internalFlagValidFields{
				Variations: &map[string]*any{
					"A": new(any("A")),
					"B": new(any("B")),
				},
				Metadata: &map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
				DefaultRule: &flag.Rule{
					Name: new(nothingToReturn),
					ProgressiveRollout: &flag.ProgressiveRollout{
						Initial: &flag.ProgressiveRolloutStep{
							Variation:  new("A"),
							Percentage: new(float64(30)),
							Date:       new(fixedNow().Add(-2 * time.Second)),
						},
						End: &flag.ProgressiveRolloutStep{
							Variation:  new("A"),
							Percentage: new(float64(20)),
							Date:       new(fixedNow().Add(2 * time.Second)),
						},
					},
				},
			},
			errorMsg: "invalid progressive rollout, initial percentage should be lower than end percentage: 30/20",
			wantErr:  assert.Error,
		},
		{
			name: "ignore invalid rule if disabled",
			fields: internalFlagValidFields{
				Variations: &map[string]*any{
					"A": new(any("A")),
					"B": new(any("B")),
				},
				Metadata: &map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
				Rules: &[]flag.Rule{
					{
						Name:    new(ruleOne),
						Query:   new(keyEqFive),
						Disable: new(true),
						Percentages: &map[string]float64{
							"A": 90,
							"B": 20,
						},
					},
				},
				DefaultRule: &flag.Rule{
					Name:            new(nothingToReturn),
					VariationResult: new("A"),
				},
			},
			errorMsg: "",
			wantErr:  assert.NoError,
		},
		{
			name: "should error if default rule referencing a variation that does not exist",
			fields: internalFlagValidFields{
				Variations: &map[string]*any{
					"A": new(any("A")),
					"B": new(any("B")),
				},
				DefaultRule: &flag.Rule{
					VariationResult: new("C"),
				},
			},
			errorMsg: "invalid variation: C does not exist",
			wantErr:  assert.Error,
		},
		{
			name: "should error if default percentage rule referencing a variation that does not exist",
			fields: internalFlagValidFields{
				Variations: &map[string]*any{
					"A": new(any("A")),
					"B": new(any("B")),
				},
				DefaultRule: &flag.Rule{
					Percentages: &map[string]float64{
						"A": 90,
						"B": 5,
						"C": 5,
					},
				},
			},
			errorMsg: "invalid percentage: variation C does not exist",
			wantErr:  assert.Error,
		},
	})
}

func TestInternalFlag_IsValid_RolloutAndQueryErrors(t *testing.T) {
	t.Parallel()

	runInternalFlagValidTests(t, []internalFlagValidCase{
		{
			name: "should error if default progressive rule end rollout step referencing a variation that does not exist",
			fields: internalFlagValidFields{
				Variations: &map[string]*any{
					"A": new(any("A")),
					"B": new(any("B")),
				},
				DefaultRule: &flag.Rule{
					ProgressiveRollout: &flag.ProgressiveRollout{
						Initial: &flag.ProgressiveRolloutStep{
							Variation:  new("A"),
							Percentage: new(float64(0)),
							Date:       new(fixedNow().Add(1 * time.Second)),
						},
						End: &flag.ProgressiveRolloutStep{
							Variation:  new("C"),
							Percentage: new(float64(100)),
							Date:       new(fixedNow().Add(2 * time.Second)),
						},
					},
				},
			},
			errorMsg: "invalid progressive rollout, end variation C does not exist",
			wantErr:  assert.Error,
		},
		{
			name: "should error if default progressive rule initial rollout step referencing a variation that does not exist",
			fields: internalFlagValidFields{
				Variations: &map[string]*any{
					"A": new(any("A")),
					"B": new(any("B")),
				},
				DefaultRule: &flag.Rule{
					ProgressiveRollout: &flag.ProgressiveRollout{
						Initial: &flag.ProgressiveRolloutStep{
							Variation:  new("C"),
							Percentage: new(float64(0)),
							Date:       new(fixedNow().Add(1 * time.Second)),
						},
						End: &flag.ProgressiveRolloutStep{
							Variation:  new("A"),
							Percentage: new(float64(100)),
							Date:       new(fixedNow().Add(2 * time.Second)),
						},
					},
				},
			},
			errorMsg: "invalid progressive rollout, initial variation C does not exist",
			wantErr:  assert.Error,
		},
		{
			name: "should error if targeting rule referencing a variation that does not exist",
			fields: internalFlagValidFields{
				Variations: &map[string]*any{
					"A": new(any("A")),
					"B": new(any("B")),
				},
				Rules: &[]flag.Rule{
					{
						Query:           new(targetingKeyEqOne),
						VariationResult: new("C"),
					},
				},
				DefaultRule: &flag.Rule{
					VariationResult: new("A"),
				},
			},
			errorMsg: "invalid variation: C does not exist",
			wantErr:  assert.Error,
		},
		{
			name: "should error if percentage in targeting rule referencing a variation that does not exist",
			fields: internalFlagValidFields{
				Variations: &map[string]*any{
					"A": new(any("A")),
					"B": new(any("B")),
				},
				Rules: &[]flag.Rule{
					{
						Query: new(targetingKeyEqOne),
						Percentages: &map[string]float64{
							"A": 90,
							"B": 5,
							"C": 5,
						},
					},
				},
				DefaultRule: &flag.Rule{
					VariationResult: new("A"),
				},
			},
			errorMsg: "invalid percentage: variation C does not exist",
			wantErr:  assert.Error,
		},
		{
			name: "should error if progressive rollout in targeting rule referencing an initial variation that does not exist",
			fields: internalFlagValidFields{
				Variations: &map[string]*any{
					"A": new(any("A")),
					"B": new(any("B")),
				},
				Rules: &[]flag.Rule{
					{
						Query: new(targetingKeyEqOne),
						ProgressiveRollout: &flag.ProgressiveRollout{
							Initial: &flag.ProgressiveRolloutStep{
								Variation:  new("C"),
								Percentage: new(float64(0)),
								Date:       new(fixedNow().Add(1 * time.Second)),
							},
							End: &flag.ProgressiveRolloutStep{
								Variation:  new("A"),
								Percentage: new(float64(100)),
								Date:       new(fixedNow().Add(2 * time.Second)),
							},
						},
					},
				},
				DefaultRule: &flag.Rule{
					VariationResult: new("A"),
				},
			},
			errorMsg: "invalid progressive rollout, initial variation C does not exist",
			wantErr:  assert.Error,
		},
		{
			name: "should error if progressive rollout in targeting rule referencing an end variation that does not exist",
			fields: internalFlagValidFields{
				Variations: &map[string]*any{
					"A": new(any("A")),
					"B": new(any("B")),
				},
				Rules: &[]flag.Rule{
					{
						Query: new(targetingKeyEqOne),
						ProgressiveRollout: &flag.ProgressiveRollout{
							Initial: &flag.ProgressiveRolloutStep{
								Variation:  new("A"),
								Percentage: new(float64(0)),
								Date:       new(fixedNow().Add(1 * time.Second)),
							},
							End: &flag.ProgressiveRolloutStep{
								Variation:  new("C"),
								Percentage: new(float64(100)),
								Date:       new(fixedNow().Add(2 * time.Second)),
							},
						},
					},
				},
				DefaultRule: &flag.Rule{
					VariationResult: new("A"),
				},
			},
			errorMsg: "invalid progressive rollout, end variation C does not exist",
			wantErr:  assert.Error,
		},
		{
			name: "should error if rule query is not valid for the parser",
			fields: internalFlagValidFields{
				Variations: &map[string]*any{
					"A": new(any("A")),
					"B": new(any("B")),
				},
				Rules: &[]flag.Rule{
					{
						Query:           new("invalid"),
						VariationResult: new("A"),
					},
				},
				DefaultRule: &flag.Rule{
					VariationResult: new("A"),
				},
			},
			errorMsg: "invalid query: parsing the query: invalid rule \"invalid\": " +
				"line 1:7 no viable alternative at input 'invalid'",
			wantErr: assert.Error,
		},
	})
}

func TestInternalFlag_ApplySheduledRollout(t *testing.T) {
	t.Parallel()

	cases := 1000
	internalFlag := flag.InternalFlag{
		Variations: &map[string]*any{
			variationA: new(any(valueAUnderscore)),
			variationB: new(any(valueBUnderscore)),
		},
		DefaultRule: &flag.Rule{
			VariationResult: new(variationA),
		},
		Metadata: &map[string]any{
			fixtures.Description: thisIsAFlag,
			fixtures.IssueLink:   httpsIssueLinkGOFF,
		},
		Scheduled: &[]flag.ScheduledStep{
			{
				DefaultRule: &flag.Rule{
					VariationResult: new(variationB),
				},
				Date: new(clocktest.Instant().Add(-2 * time.Second)),
			},
			{
				Variations: &map[string]*any{
					variationB: new(any(valueQWERTY)),
				},
				Date: new(clocktest.Instant().Add(-1 * time.Second)),
			},
		},
	}

	for range cases {
		t.Run("scheduledRollout", func(t *testing.T) {
			t.Parallel()

			wantResult := valueQWERTY
			wantDetails := flag.ResolutionDetails{
				Variant: variationB,
				Reason:  flag.ReasonStatic,
				Metadata: map[string]any{
					fixtures.Description: thisIsAFlag,
					fixtures.IssueLink:   httpsIssueLinkGOFF,
				},
			}

			got, got1 := internalFlag.Value(
				fixtures.FlagKeyBare,
				ffcontext.NewEvaluationContextBuilder(fixtures.UserKeyUser).Build(),
				flag.Context{Service: flag.NewService(),
					DefaultSdkValue: valueDefault,
				},
			)
			assert.Equalf(t, wantResult, got, notExpectedValueS, cmp.Diff(wantResult, got))
			assert.Equalf(
				t,
				wantDetails,
				got1,
				notExpectedValueS,
				cmp.Diff(wantDetails, got1),
			)
		})
	}
}

func TestInternalFlag_GetVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		flag flag.InternalFlag
		want string
	}{
		{
			name: "Should return empty string if version is nil",
			flag: flag.InternalFlag{
				Version: nil,
			},
			want: "",
		},
		{
			name: "Should return version if not nil",
			flag: flag.InternalFlag{
				Version: new(fixtures.VersionOneZeroZero),
			},
			want: fixtures.VersionOneZeroZero,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equalf(t, tt.want, tt.flag.GetVersion(), "GetVersion()")
		})
	}
}

// Standalone (rule 7 exception): this asserts repeated-run determinism, which cannot share the
// table-driven setup of TestInternalFlag_IsValid.
func TestInternalFlag_IsValid_DeterministicError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		variations map[string]*any
		wantErr    string
	}{
		{
			name: "type mismatch reported before later nil value",
			variations: map[string]*any{
				"a": new(any(true)),
				"b": new(any("str")),
				"c": nil,
				"d": new(any(12)),
			},
			wantErr: "all variations should have the same type",
		},
		{
			name: "first nil variation in sorted order is reported",
			variations: map[string]*any{
				"a": new(any(true)),
				"b": nil,
				"c": nil,
				"d": nil,
				"e": new(any("str")),
			},
			wantErr: "nil value for variation: b",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			for range 50 {
				f := &flag.InternalFlag{
					Variations:  &tt.variations,
					DefaultRule: &flag.Rule{VariationResult: new("a")},
				}
				assert.EqualError(t, f.IsValid(flag.NewService()), tt.wantErr)
			}
		})
	}
}

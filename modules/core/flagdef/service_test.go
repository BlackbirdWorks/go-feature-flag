package flagdef_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
	"github.com/blackbirdworks/semaphore/modules/core/testhelpers/clocktest"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

func serviceTestFlag() *flagdef.InternalFlag {
	return &flagdef.InternalFlag{
		Variations: &map[string]*any{"on": new(any(true)), fixtures.VariationOff: new(any(false))},
		Rules: &[]flagdef.Rule{{
			Name:            new(targetingKey),
			Query:           new(`plan eq "pro"`),
			VariationResult: new("on"),
		}},
		DefaultRule: &flagdef.Rule{VariationResult: new(fixtures.VariationOff)},
	}
}

func TestInternalFlag_Value_RequiresTheService(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantValue     any
		service       *flagdef.Service
		name          string
		wantErrorCode flagdef.ErrorCode
		wantVariant   string
	}{
		{
			name:          "no Service is an error, not a throwaway Service",
			service:       nil,
			wantErrorCode: flagdef.ErrorCodeGeneral,
			wantVariant:   flagdef.VariationSDKDefault,
			wantValue:     "sdk-default",
		},
		{
			name:          "a Service evaluates the flag",
			service:       flagdef.NewService(),
			wantErrorCode: "",
			wantVariant:   "on",
			wantValue:     true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := serviceTestFlag()
			ctx := ffcontext.NewEvaluationContextBuilder(fixtures.ContextKindUser).
				AddCustom(fixtures.AttrPlan, fixtures.PlanPro).
				Build()

			got, details := f.Value(
				fixtures.FlagKeyBare,
				ctx,
				flagdef.Context{DefaultSdkValue: "sdk-default", Service: tt.service},
			)

			assert.Equal(t, tt.wantValue, got)
			assert.Equal(t, tt.wantVariant, details.Variant)
			assert.Equal(t, tt.wantErrorCode, details.ErrorCode)
		})
	}
}

func TestRule_Evaluate_RequiresTheService(t *testing.T) {
	t.Parallel()

	tests := []struct {
		service *flagdef.Service
		name    string
		want    string
		wantErr bool
	}{
		{name: "no Service", service: nil, wantErr: true, want: ""},
		{name: "a Service", service: flagdef.NewService(), wantErr: false, want: "on"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := flagdef.Rule{Query: new(`plan eq "pro"`), VariationResult: new("on")}
			ctx := ffcontext.NewEvaluationContextBuilder(fixtures.ContextKindUser).
				AddCustom(fixtures.AttrPlan, fixtures.PlanPro).
				Build()

			got, err := r.Evaluate(tt.service, fixtures.ContextKindUser, ctx, fixtures.FlagKeyBare, false)

			assert.Equal(t, tt.want, got)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// TestInternalFlag_Value_EvaluationDateOnlyMattersForDynamicFlags pins the behaviour that the lazy read of the
// evaluation date must keep: a flag without scheduled steps, experimentation or progressive rollout gives the
// same result at every date (its queries read the context attributes, not the date), and the flags that depend
// on the date still do.
func TestInternalFlag_Value_EvaluationDateOnlyMattersForDynamicFlags(t *testing.T) {
	t.Parallel()

	early := clocktest.Instant()
	late := early.AddDate(10, 0, 0)
	experiment := func() *flagdef.InternalFlag {
		f := serviceTestFlag()
		f.Experimentation = &flagdef.ExperimentationRollout{End: new(early.Add(time.Hour))}

		return f
	}

	tests := []struct {
		name      string
		flag      *flagdef.InternalFlag
		wantEarly string
		wantLate  string
	}{
		{name: "static flag with a targeting rule", flag: serviceTestFlag(), wantEarly: "on", wantLate: "on"},
		{
			name: "static flag with a rule on the date attribute of the context",
			flag: &flagdef.InternalFlag{
				Variations: &map[string]*any{"on": new(any(true)), fixtures.VariationOff: new(any(false))},
				Rules: &[]flagdef.Rule{{
					Query: new(`semaphore.currentDateTime ne ""`), VariationResult: new("on"),
				}},
				DefaultRule: &flagdef.Rule{VariationResult: new(fixtures.VariationOff)},
			},
			wantEarly: "on", wantLate: "on",
		},
		{
			name:      "experimentation over at the late date",
			flag:      experiment(),
			wantEarly: "on",
			wantLate:  flagdef.VariationSDKDefault,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := flagdef.NewService()

			base := ffcontext.NewEvaluationContextBuilder(fixtures.ContextKindUser).
				AddCustom(fixtures.AttrPlan, fixtures.PlanPro).
				Build()
			for date, want := range map[time.Time]string{early: tt.wantEarly, late: tt.wantLate} {
				ctx := withDate(base, date)
				_, details := tt.flag.Value(fixtures.FlagKeyBare, ctx, flagdef.Context{Service: svc})
				assert.Equal(t, want, details.Variant, "variant at %s", date)
			}
		})
	}
}

// withDate returns a copy of ctx carrying date as its currentDateTime.
func withDate(ctx ffcontext.Context, date time.Time) ffcontext.EvaluationContext {
	out := ffcontext.NewEvaluationContext(ctx.GetKey())
	for k, v := range ctx.GetCustom() {
		out.AddCustomAttribute(k, v)
	}

	out.AddCustomAttribute(ffcontext.ReservedKey, map[string]any{fixtures.CurrentDateTime: date.Format(time.RFC3339)})

	return out
}

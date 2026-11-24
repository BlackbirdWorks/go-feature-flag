package flag_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/flag"
	"github.com/blackbirdworks/semaphore/modules/core/testutils/clocktest"
)

func serviceTestFlag() *flag.InternalFlag {
	return &flag.InternalFlag{
		Variations: &map[string]*any{"on": new(any(true)), "off": new(any(false))},
		Rules: &[]flag.Rule{{
			Name:            new("targeting"),
			Query:           new(`plan eq "pro"`),
			VariationResult: new("on"),
		}},
		DefaultRule: &flag.Rule{VariationResult: new("off")},
	}
}

func TestInternalFlag_Value_RequiresTheService(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		service       *flag.Service
		wantErrorCode flag.ErrorCode
		wantVariant   string
		wantValue     any
	}{
		{
			name:          "no Service is an error, not a throwaway Service",
			service:       nil,
			wantErrorCode: flag.ErrorCodeGeneral,
			wantVariant:   flag.VariationSDKDefault,
			wantValue:     "sdk-default",
		},
		{
			name:          "a Service evaluates the flag",
			service:       flag.NewService(),
			wantErrorCode: "",
			wantVariant:   "on",
			wantValue:     true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := serviceTestFlag()
			ctx := ffcontext.NewEvaluationContextBuilder("user").AddCustom("plan", "pro").Build()

			got, details := f.Value("flag", ctx, flag.Context{DefaultSdkValue: "sdk-default", Service: tt.service})

			assert.Equal(t, tt.wantValue, got)
			assert.Equal(t, tt.wantVariant, details.Variant)
			assert.Equal(t, tt.wantErrorCode, details.ErrorCode)
		})
	}
}

func TestRule_Evaluate_RequiresTheService(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		service *flag.Service
		wantErr bool
		want    string
	}{
		{name: "no Service", service: nil, wantErr: true, want: ""},
		{name: "a Service", service: flag.NewService(), wantErr: false, want: "on"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := flag.Rule{Query: new(`plan eq "pro"`), VariationResult: new("on")}
			ctx := ffcontext.NewEvaluationContextBuilder("user").AddCustom("plan", "pro").Build()

			got, err := r.Evaluate(tt.service, "user", ctx, "flag", false)

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
	experiment := func() *flag.InternalFlag {
		f := serviceTestFlag()
		f.Experimentation = &flag.ExperimentationRollout{End: new(early.Add(time.Hour))}

		return f
	}
	tests := []struct {
		name      string
		flag      *flag.InternalFlag
		wantEarly string
		wantLate  string
	}{
		{name: "static flag with a targeting rule", flag: serviceTestFlag(), wantEarly: "on", wantLate: "on"},
		{
			name: "static flag with a rule on the date attribute of the context",
			flag: &flag.InternalFlag{
				Variations: &map[string]*any{"on": new(any(true)), "off": new(any(false))},
				Rules: &[]flag.Rule{{
					Query: new(`semaphore.currentDateTime ne ""`), VariationResult: new("on"),
				}},
				DefaultRule: &flag.Rule{VariationResult: new("off")},
			},
			wantEarly: "on", wantLate: "on",
		},
		{
			name:      "experimentation over at the late date",
			flag:      experiment(),
			wantEarly: "on",
			wantLate:  flag.VariationSDKDefault,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := flag.NewService()
			base := ffcontext.NewEvaluationContextBuilder("user").AddCustom("plan", "pro").Build()
			for date, want := range map[time.Time]string{early: tt.wantEarly, late: tt.wantLate} {
				ctx := flag.EnrichContext(base, nil)
				ctx = withDate(ctx, date)
				_, details := tt.flag.Value("flag", ctx, flag.Context{Service: svc})
				assert.Equal(t, want, details.Variant, "variant at %s", date)
			}
		})
	}
}

// withDate returns a copy of ctx carrying date as its currentDateTime.
func withDate(ctx ffcontext.Context, date time.Time) ffcontext.Context {
	out := ffcontext.NewEvaluationContext(ctx.GetKey())
	for k, v := range ctx.GetCustom() {
		out.AddCustomAttribute(k, v)
	}
	out.AddCustomAttribute(ffcontext.ReservedKey, map[string]any{"currentDateTime": date.Format(time.RFC3339)})

	return out
}

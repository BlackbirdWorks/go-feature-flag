package flag_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/flag"
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

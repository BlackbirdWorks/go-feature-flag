package evaluation_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/evaluation"
	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
	"github.com/blackbirdworks/semaphore/modules/core/model"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

const (
	targetingKeyUUID = "e22a40e0-28fa-413c-aef8-4dc6a27593b7"
)

func Test_evaluate(t *testing.T) {
	t.Parallel()

	type args struct {
		flagCtx         flagdef.Context
		flag            flagdef.Flag
		evaluationCtx   ffcontext.Context
		sdkDefaultValue any
		flagKey         string
		expectedType    string
	}

	tests := []struct {
		args         args
		want         model.VariationResult[any]
		errAssertion assert.ErrorAssertionFunc
		name         string
	}{
		{
			name: "Test with a rule name",
			args: args{
				flag: &flagdef.InternalFlag{
					Variations: &map[string]*any{
						"on":                  new(any(true)),
						fixtures.VariationOff: new(any(false)),
					},
					DefaultRule: &flagdef.Rule{
						VariationResult: new("on"),
					},
				},
				flagKey:         fixtures.FlagKeyMy,
				evaluationCtx:   ffcontext.NewEvaluationContext(targetingKeyUUID),
				flagCtx:         flagdef.Context{Service: flagdef.NewService()},
				expectedType:    "bool",
				sdkDefaultValue: false,
			},
			want: model.VariationResult[any]{
				TrackEvents:   true,
				VariationType: "on",
				Failed:        false,
				Reason:        flagdef.ReasonStatic,
				Value:         true,
				Cacheable:     true,
			},
			errAssertion: assert.NoError,
		},
		{
			name: "Test with nil value",
			args: args{
				flag: &flagdef.InternalFlag{
					Variations: &map[string]*any{
						"on":                  nil,
						fixtures.VariationOff: nil,
					},
					DefaultRule: &flagdef.Rule{
						VariationResult: new("on"),
					},
				},
				flagKey:         fixtures.FlagKeyMy,
				evaluationCtx:   ffcontext.NewEvaluationContext(targetingKeyUUID),
				flagCtx:         flagdef.Context{Service: flagdef.NewService()},
				expectedType:    "bool",
				sdkDefaultValue: false,
			},
			want: model.VariationResult[any]{
				VariationType: "on",
				Failed:        false,
				Reason:        flagdef.ReasonStatic,
				Value:         nil,
				Cacheable:     true,
				TrackEvents:   true,
			},
			errAssertion: assert.NoError,
		},
		{
			name: "Test with float64 value and int expected",
			args: args{
				flag: &flagdef.InternalFlag{
					Variations: &map[string]*any{
						"on":                  new(any(float64(3))),
						fixtures.VariationOff: new(any(float64(0))),
					},
					DefaultRule: &flagdef.Rule{
						VariationResult: new("on"),
					},
				},
				flagKey:         fixtures.FlagKeyMy,
				evaluationCtx:   ffcontext.NewEvaluationContext(targetingKeyUUID),
				flagCtx:         flagdef.Context{Service: flagdef.NewService()},
				expectedType:    fixtures.IntType,
				sdkDefaultValue: 0,
			},
			want: model.VariationResult[any]{
				VariationType: "on",
				Failed:        false,
				Reason:        flagdef.ReasonStatic,
				Value:         3,
				Cacheable:     true,
				TrackEvents:   true,
			},
			errAssertion: assert.NoError,
		},
		{
			name: "Test with float64 value and int expected",
			args: args{
				flag: &flagdef.InternalFlag{
					Variations: &map[string]*any{
						"on":                  new(any(float64(3))),
						fixtures.VariationOff: new(any(float64(0))),
					},
					DefaultRule: &flagdef.Rule{
						VariationResult: new("on"),
					},
				},
				flagKey:         fixtures.FlagKeyMy,
				evaluationCtx:   ffcontext.NewEvaluationContext(targetingKeyUUID),
				flagCtx:         flagdef.Context{Service: flagdef.NewService()},
				expectedType:    fixtures.IntType,
				sdkDefaultValue: 0,
			},
			want: model.VariationResult[any]{
				VariationType: "on",
				Failed:        false,
				Reason:        flagdef.ReasonStatic,
				Value:         3,
				Cacheable:     true,
				TrackEvents:   true,
			},
			errAssertion: assert.NoError,
		},
		{
			name: "Test with float64 value and float expected",
			args: args{
				flag: &flagdef.InternalFlag{
					Variations: &map[string]*any{
						"on":                  new(any(float64(3.5))),
						fixtures.VariationOff: new(any(float64(0.3))),
					},
					DefaultRule: &flagdef.Rule{
						VariationResult: new("on"),
					},
				},
				flagKey:         fixtures.FlagKeyMy,
				evaluationCtx:   ffcontext.NewEvaluationContext(targetingKeyUUID),
				flagCtx:         flagdef.Context{Service: flagdef.NewService()},
				expectedType:    "float",
				sdkDefaultValue: 0,
			},
			want: model.VariationResult[any]{
				VariationType: "on",
				Failed:        false,
				Reason:        flagdef.ReasonStatic,
				Value:         3.5,
				Cacheable:     true,
				TrackEvents:   true,
			},
			errAssertion: assert.NoError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := evaluation.Evaluate(
				tt.args.flag,
				tt.args.flagKey,
				tt.args.evaluationCtx,
				tt.args.flagCtx,
				tt.args.expectedType,
				tt.args.sdkDefaultValue,
			)
			tt.errAssertion(t, err)

			if err == nil {
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func Test_evaluate_typeMisMatch(t *testing.T) {
	t.Parallel()

	targetFlag := &flagdef.InternalFlag{
		Variations: &map[string]*any{
			"on":                  new(any(true)),
			fixtures.VariationOff: new(any(false)),
		},
		DefaultRule: &flagdef.Rule{
			VariationResult: new("on"),
		},
	}
	got, err := evaluation.Evaluate[int](
		targetFlag,
		fixtures.FlagKeyMy,
		ffcontext.NewEvaluationContext(targetingKeyUUID),
		flagdef.Context{Service: flagdef.NewService()},
		fixtures.IntType,
		42,
	)
	require.Error(t, err)

	want := model.VariationResult[int]{
		VariationType: flagdef.VariationSDKDefault,
		Failed:        true,
		Reason:        flagdef.ReasonError,
		ErrorCode:     flagdef.ErrorCodeTypeMismatch,
		Value:         42,
		TrackEvents:   true,
	}
	assert.Equal(t, want, got)
}

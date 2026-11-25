package utils_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/utils"
)

const (
	companyId       = "company_id"
	companyName     = "company_name"
	fixture2323F37B = "2323f37b-eef7-4bbc-856f-7d16c67de3ae"
	goFeatureFlag   = "go feature flag"
)

func Test_ConvertEvaluationCtxFromReq(t *testing.T) {
	t.Parallel()

	type fields struct {
		Custom map[string]any
		Key    string
	}

	tests := []struct {
		fields fields
		want   ffcontext.Context
		name   string
	}{
		{
			name: "simple case",
			fields: fields{
				Key: fixture2323F37B,
				Custom: map[string]any{
					companyName: goFeatureFlag,
				},
			},
			want: ffcontext.
				NewEvaluationContextBuilder(fixture2323F37B).
				AddCustom(companyName, goFeatureFlag).
				Build(),
		},
		{
			name: "should return a float if the value is a float",
			fields: fields{
				Key: fixture2323F37B,
				Custom: map[string]any{
					companyName: goFeatureFlag,
					companyId:   1.1,
				},
			},
			want: ffcontext.
				NewEvaluationContextBuilder(fixture2323F37B).
				AddCustom(companyName, goFeatureFlag).
				AddCustom(companyId, 1.1).
				Build(),
		},
		{
			name: "should return an int if the value is a float but without decimal",
			fields: fields{
				Key: fixture2323F37B,
				Custom: map[string]any{
					companyName: goFeatureFlag,
					companyId:   1.0,
				},
			},
			want: ffcontext.
				NewEvaluationContextBuilder(fixture2323F37B).
				AddCustom(companyName, goFeatureFlag).
				AddCustom(companyId, 1).
				Build(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := utils.ConvertEvaluationCtxFromRequest(tt.fields.Key, tt.fields.Custom)
			assert.Equal(t, tt.want, got)
		})
	}
}

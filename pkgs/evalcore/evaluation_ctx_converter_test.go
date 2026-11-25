package evalcore_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
	"github.com/blackbirdworks/semaphore/pkgs/evalcore"
)

const (
	companyID      = "company_id"
	companyName    = "company_name"
	contextKeyUUID = "2323f37b-eef7-4bbc-856f-7d16c67de3ae"
	goFeatureFlag  = "go feature flag"
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
				Key: contextKeyUUID,
				Custom: map[string]any{
					companyName: goFeatureFlag,
				},
			},
			want: ffcontext.
				NewEvaluationContextBuilder(contextKeyUUID).
				AddCustom(companyName, goFeatureFlag).
				Build(),
		},
		{
			name: "should return a float if the value is a float",
			fields: fields{
				Key: contextKeyUUID,
				Custom: map[string]any{
					companyName: goFeatureFlag,
					companyID:   1.1,
				},
			},
			want: ffcontext.
				NewEvaluationContextBuilder(contextKeyUUID).
				AddCustom(companyName, goFeatureFlag).
				AddCustom(companyID, 1.1).
				Build(),
		},
		{
			name: "should return an int if the value is a float but without decimal",
			fields: fields{
				Key: contextKeyUUID,
				Custom: map[string]any{
					companyName: goFeatureFlag,
					companyID:   1.0,
				},
			},
			want: ffcontext.
				NewEvaluationContextBuilder(contextKeyUUID).
				AddCustom(companyName, goFeatureFlag).
				AddCustom(companyID, 1).
				Build(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := evalcore.ConvertEvaluationCtxFromRequest(tt.fields.Key, tt.fields.Custom)
			assert.Equal(t, tt.want, got)
		})
	}
}

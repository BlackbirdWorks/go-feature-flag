package utils_test

import (
	"testing"

	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/utils"
	"github.com/stretchr/testify/assert"
)

func TestUserToMap(t *testing.T) {
	tests := []struct {
		name string
		u    ffcontext.Context
		want map[string]any
	}{
		{
			name: "complete user",
			u: ffcontext.NewEvaluationContextBuilder("key").
				AddCustom("anonymous", false).
				AddCustom("email", "contact@gofeatureflag.org").
				Build(),
			want: map[string]any{
				"key":          "key",
				"anonymous":    false,
				"email":        "contact@gofeatureflag.org",
				"targetingKey": "key",
			},
		},
		{
			name: "anonymous user",
			u: ffcontext.NewEvaluationContextBuilder("key").
				AddCustom("anonymous", true).
				Build(),
			want: map[string]any{
				"key":          "key",
				"anonymous":    true,
				"targetingKey": "key",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, utils.ContextToMap(tt.u))
		})
	}
}

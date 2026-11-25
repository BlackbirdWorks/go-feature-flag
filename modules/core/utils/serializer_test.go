package utils_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/utils"
)

const (
	anonymous2 = "anonymous"
	key2       = "key"
)

func TestUserToMap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		u    ffcontext.Context
		want map[string]any
		name string
	}{
		{
			name: "complete user",
			u: ffcontext.NewEvaluationContextBuilder(key2).
				AddCustom(anonymous2, false).
				AddCustom("email", "contact@gofeatureflag.org").
				Build(),
			want: map[string]any{
				key2:           key2,
				anonymous2:     false,
				"email":        "contact@gofeatureflag.org",
				"targetingKey": key2,
			},
		},
		{
			name: "anonymous user",
			u: ffcontext.NewEvaluationContextBuilder(key2).
				AddCustom(anonymous2, true).
				Build(),
			want: map[string]any{
				key2:           key2,
				anonymous2:     true,
				"targetingKey": key2,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, utils.ContextToMap(tt.u))
		})
	}
}

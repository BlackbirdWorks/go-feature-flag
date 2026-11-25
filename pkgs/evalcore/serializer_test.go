package evalcore_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
	"github.com/blackbirdworks/semaphore/pkgs/evalcore"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
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
			u: ffcontext.NewEvaluationContextBuilder(fixtures.MetadataKey).
				AddCustom(fixtures.Anonymous, false).
				AddCustom("email", "contact@gofeatureflag.org").
				Build(),
			want: map[string]any{
				fixtures.MetadataKey: fixtures.MetadataKey,
				fixtures.Anonymous:   false,
				"email":              "contact@gofeatureflag.org",
				"targetingKey":       fixtures.MetadataKey,
			},
		},
		{
			name: "anonymous user",
			u: ffcontext.NewEvaluationContextBuilder(fixtures.MetadataKey).
				AddCustom(fixtures.Anonymous, true).
				Build(),
			want: map[string]any{
				fixtures.MetadataKey: fixtures.MetadataKey,
				fixtures.Anonymous:   true,
				"targetingKey":       fixtures.MetadataKey,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, evalcore.ContextToMap(tt.u))
		})
	}
}

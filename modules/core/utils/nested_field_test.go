package utils_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/utils"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

const (
	alice = "alice"
)

func TestGetNestedFieldValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		want    any
		ctx     map[string]any
		name    string
		key     string
		wantErr bool
	}{
		{
			name: "simple nested string",
			ctx: map[string]any{
				fixtures.ContextKindUser: map[string]any{
					"name": alice,
				},
			},
			key:  "user.name",
			want: alice,
		},
		{
			name: "nested number",
			ctx: map[string]any{
				"metrics": map[string]any{
					"score": 42,
				},
			},
			key:  "metrics.score",
			want: float64(42), // JSON numbers become float64
		},
		{
			name: "nested bool",
			ctx: map[string]any{
				"flags": map[string]any{
					"beta": true,
				},
			},
			key:  "flags.beta",
			want: true,
		},
		{
			name: "missing key returns error",
			ctx: map[string]any{
				"a": map[string]any{"b": 1},
			},
			key:     "a.c",
			wantErr: true,
		},
		{
			name:    "empty key returns error",
			ctx:     map[string]any{"a": 1},
			key:     "",
			wantErr: true,
		},
		{
			name:    "nil context returns error",
			ctx:     nil,
			key:     "a.b",
			wantErr: true,
		},
		{
			name: "trying to navigate into non-map value returns error",
			ctx: map[string]any{
				fixtures.ContextKindUser: map[string]any{
					"name": alice, // name is a string, not a map
				},
			},
			key:     "user.name.first", // trying to access .first on a string
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := utils.GetNestedFieldValue(tt.ctx, tt.key)
			if tt.wantErr {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)
			require.EqualValues(t, tt.want, got)
		})
	}
}

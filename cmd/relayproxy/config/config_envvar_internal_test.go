package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

const (
	kindKey            = "kind"
	pathKey            = "path"
	retrieversZeroPath = "RETRIEVERS_0_PATH"
	retrieversKey      = "retrievers"
)

func TestLoadArrayEnv_ConfigShapes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		config    map[string]any
		want      map[string]any
		name      string
		key       string
		wantErrIn string
		wantErr   bool
	}{
		{
			name:   "map entry: the field is set on it",
			key:    retrieversZeroPath,
			config: map[string]any{retrieversKey: []any{map[string]any{kindKey: fixtures.File}}},
			want:   map[string]any{retrieversKey: []any{map[string]any{kindKey: fixtures.File, pathKey: "v"}}},
		},
		{
			name:   "index beyond the length: the array is expanded",
			key:    "RETRIEVERS_2_PATH",
			config: map[string]any{retrieversKey: []any{map[string]any{kindKey: fixtures.File}}},
			want: map[string]any{retrieversKey: []any{
				map[string]any{kindKey: fixtures.File}, map[string]any{}, map[string]any{pathKey: "v"},
			}},
		},
		{
			name:   "no array yet: it is created",
			key:    retrieversZeroPath,
			config: map[string]any{},
			want:   map[string]any{retrieversKey: []any{map[string]any{pathKey: "v"}}},
		},
		{
			name:   "nil entry: replaced by a new map",
			key:    retrieversZeroPath,
			config: map[string]any{retrieversKey: []any{nil}},
			want:   map[string]any{retrieversKey: []any{map[string]any{pathKey: "v"}}},
		},
		{
			name:      "scalar entry: an error, no panic",
			key:       retrieversZeroPath,
			config:    map[string]any{retrieversKey: []any{"oops"}},
			wantErr:   true,
			wantErrIn: `environment variable "RETRIEVERS_0_PATH": the config entry at index 0 is a string, expected an object`,
		},
		{
			name:      "nested array entry: an error, no panic",
			key:       "RETRIEVERS_1_PATH",
			config:    map[string]any{retrieversKey: []any{map[string]any{}, []any{"a"}}},
			wantErr:   true,
			wantErrIn: "the config entry at index 1 is a []interface {}, expected an object",
		},
		{
			name:      "negative index: an error, no panic",
			key:       "RETRIEVERS_-1_PATH",
			config:    map[string]any{retrieversKey: []any{map[string]any{}}},
			wantErr:   true,
			wantErrIn: "negative index -1",
		},
		{
			name:      "no field: an error, no panic",
			key:       "RETRIEVERS_0",
			config:    map[string]any{},
			wantErr:   true,
			wantErrIn: "expected <ARRAY>_<INDEX>_<FIELD>",
		},
		{
			name:      "no index: an error, no panic",
			key:       "RETRIEVERS",
			config:    map[string]any{},
			wantErr:   true,
			wantErrIn: "expected <ARRAY>_<INDEX>_<FIELD>",
		},
		{
			name:      "index that is not a number: an error",
			key:       "RETRIEVERS_X_PATH",
			config:    map[string]any{},
			wantErr:   true,
			wantErrIn: `invalid index "x"`,
		},
		{
			name: "flagset nested array with a scalar entry: an error, no panic",
			key:  "FLAGSETS_0_RETRIEVERS_0_PATH",
			config: map[string]any{"flagsets": []any{
				map[string]any{retrieversKey: []any{42}},
			}},
			wantErr:   true,
			wantErrIn: "the config entry at index 0 is a int, expected an object",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var (
				got map[string]any
				err error
			)

			require.NotPanics(t, func() { got, err = loadArrayEnv(tt.key, "v", tt.config) })

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErrIn)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

package flagdef_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
)

// asMap and asSlice read a fixture container without failing the goroutine of the caller.
func asMap(t *testing.T, v any) map[string]any {
	t.Helper()

	m, ok := v.(map[string]any)
	assert.True(t, ok)

	return m
}

func asSlice(t *testing.T, v any) []any {
	t.Helper()

	s, ok := v.([]any)
	assert.True(t, ok)

	return s
}

func TestCloneValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		value  any
		want   any
		change func(t *testing.T, clone any)
		name   string
	}{
		{name: "null", value: nil, want: nil},
		{name: "string", value: "a", want: "a"},
		{name: "number", value: 1.5, want: 1.5},
		{
			name:  "object",
			value: map[string]any{"a": map[string]any{"b": []any{"c"}}},
			change: func(t *testing.T, c any) {
				t.Helper()

				asSlice(t, asMap(t, asMap(t, c)["a"])["b"])[0] = "z"
			},
			want: map[string]any{"a": map[string]any{"b": []any{"c"}}},
		},
		{
			name:  "array",
			value: []any{map[string]any{"a": "b"}},
			change: func(t *testing.T, c any) {
				t.Helper()

				asMap(t, asSlice(t, c)[0])["a"] = "z"
			},
			want: []any{map[string]any{"a": "b"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			clone := flagdef.CloneValue(tt.value)
			assert.Equal(t, tt.want, clone)

			if tt.change != nil {
				tt.change(t, clone)
				assert.Equal(t, tt.want, tt.value, "the original was changed through the clone")
			}
		})
	}
}

func TestCloneMetadata(t *testing.T) {
	t.Parallel()

	tests := []struct {
		metadata map[string]any
		want     map[string]any
		name     string
	}{
		{name: "null", metadata: nil, want: nil},
		{name: "empty", metadata: map[string]any{}, want: map[string]any{}},
		{
			name:     "nested",
			metadata: map[string]any{"a": "b", "n": map[string]any{"k": "v"}},
			want:     map[string]any{"a": "b", "n": map[string]any{"k": "v"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			clone := flagdef.CloneMetadata(tt.metadata)
			assert.Equal(t, tt.want, clone)

			if clone != nil {
				clone["added"] = true

				if nested, ok := clone["n"].(map[string]any); ok {
					nested["k"] = "changed"
				}

				assert.NotContains(t, tt.metadata, "added")
				assert.Equal(t, tt.want, tt.metadata, "the original was changed through the clone")
			}
		})
	}
}

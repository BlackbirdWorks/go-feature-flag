package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStringToArray(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    []string
		expected []string
	}{
		{
			name:     "single string with commas",
			input:    []string{"a,b,c"},
			expected: []string{"a", "b", "c"},
		},
		{
			name:     "empty slice",
			input:    []string{},
			expected: []string{},
		},
		{
			name:     "slice with one empty string",
			input:    []string{""},
			expected: []string{""},
		},
		{
			name:     "slice with multiple elements (should split only first)",
			input:    []string{"a,b", "c"},
			expected: []string{"a", "b"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := StringToArray(tt.input)
			assert.Equal(t, tt.expected, result, "StringToArray(%v)", tt.input)
		})
	}
}

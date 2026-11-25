package utils_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/modules/core/utils"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

func Test_JSONTypeExtractor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    any
		expected string
	}{
		{"string", "hello", "(string)"},
		{"integer", 42, "(number)"},
		{"float", 3.14, "(number)"},
		{"bool", true, "(bool)"},
		{"[]interface", []any{1, "two", 3.0}, "([]interface{})"},
		{"map", map[string]any{"key1": "value1", "key2": 2}, "(map[string]interface{})"},
		{fixtures.NullLiteral, nil, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.expected, utils.JSONTypeExtractor(tt.input))
		})
	}
}

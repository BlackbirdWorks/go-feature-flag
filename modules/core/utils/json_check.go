package utils

import (
	"encoding/json"
	"strings"
)

// IsJSONObject checks if input is a valid JSON.
func IsJSONObject(input string) bool {
	// Decoding into a map only succeeds for an object or for null, so skip the copy
	// and the decode when input can be neither. JSON allows only these four whitespace
	// characters before a value.
	trimmed := strings.TrimLeft(input, " \t\n\r")
	if !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "null") {
		return false
	}

	var obj map[string]any

	return json.Unmarshal([]byte(input), &obj) == nil
}

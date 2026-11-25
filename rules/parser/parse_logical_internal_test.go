package parser

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	x2AndY = `x < 2 and ( y > 4 or z == true )`
	xAEq1  = `x.a eq 1 and x.b.c eq 2`
	xAEq12 = `x.a eq 1 or x.b.c eq 2`
)

func TestNestedObject(t *testing.T) {
	t.Parallel()

	tests := []testCase{
		{
			rule: xAEq1,
			input: map[string]any{
				"x": map[string]any{
					"a": 1,
					"b": map[string]any{
						"c": 2.0,
					},
				},
			},
			result:   true,
			hasError: false,
		},
		{
			rule: xAEq1,
			input: map[string]any{
				"x": map[string]any{
					"a": 1,
					"b": map[string]any{
						"c": 2,
					},
				},
			},
			result:   true,
			hasError: false,
		},
		{
			rule: xAEq1,
			input: map[string]any{
				"x": map[string]any{
					"b": map[string]any{
						"c": 2,
					},
				},
			},
			result:   false,
			hasError: false,
		},
		{
			rule: xAEq12,
			input: map[string]any{
				"x": map[string]any{
					"b": map[string]any{
						"c": 2,
					},
				},
			},
			result:   true,
			hasError: false,
		},
		{
			rule:     xAEq12,
			input:    nil,
			result:   false,
			hasError: false,
		},
		{
			rule:     xAEq12,
			input:    obj{},
			result:   false,
			hasError: false,
		},
		{
			rule:     `x.a eq 1 or x.b.c gt true`,
			input:    obj{},
			result:   false,
			hasError: true,
		},
		{
			rule: `x.a eq 1 or x.b.c gt true`,
			input: map[string]any{
				"x": map[string]any{
					"a": 1,
				},
			},
			result:   true,
			hasError: false,
		},
		{
			rule: `x.b.c gt true or x.a eq 1`,
			input: map[string]any{
				"x": map[string]any{
					"a": 1,
				},
			},
			result:   false,
			hasError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.rule, func(t *testing.T) {
			t.Parallel()

			result, err := eval(tt.rule, tt.input)
			if tt.hasError {
				assert.Error(t, err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.result, result)
		})
	}
}

func TestIssue42VersionAndLogicalOrder(t *testing.T) {
	t.Parallel()

	input := obj{
		"x": abc,
		"y": "11.3.0",
	}
	parenInput := obj{
		"x": "2.2.0",
		"y": "z",
	}
	tests := []testCase{
		{
			rule:     `y gt 11.1.0-0 and not(x IN ["abc","cde","fgh"])`,
			input:    input,
			result:   false,
			hasError: false,
		},
		{
			rule:     `not(x IN ["abc","cde","fgh"]) and y gt 11.1.0-0`,
			input:    input,
			result:   false,
			hasError: false,
		},
		{
			rule:     `y gt 11.1.0-0 and not(x IN ["cde","fgh"])`,
			input:    input,
			result:   true,
			hasError: false,
		},
		{
			rule:     `not(x IN ["cde","fgh"]) and y gt 11.1.0-0`,
			input:    input,
			result:   true,
			hasError: false,
		},
		{
			rule:     `x gt 2.1.0-0 and not(y in ["a", "b", "c"])`,
			input:    parenInput,
			result:   true,
			hasError: false,
		},
		{
			rule:     `(x gt 2.1.0-0) and not(y in ["a", "b", "c"])`,
			input:    parenInput,
			result:   true,
			hasError: false,
		},
		{
			rule:     `((x gt 2.1.0-0) and not(y in ["a", "b", "c"]))`,
			input:    parenInput,
			result:   true,
			hasError: false,
		},
		{
			rule:     `(x gt 2.1.0-0 and not(y in ["a", "b", "c"]))`,
			input:    parenInput,
			result:   true,
			hasError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.rule, func(t *testing.T) {
			t.Parallel()

			result, err := eval(tt.rule, tt.input)
			require.NoError(t, err, tt.rule)
			assert.Equal(t, tt.result, result, tt.rule)
			assert.Equal(t, tt.result, Evaluate(tt.rule, tt.input), tt.rule)
			assert.Equal(t, tt.result, Evaluate(fmt.Sprintf(parenthesizedFormat, tt.rule), tt.input), tt.rule)
		})
	}
}

func TestLogicalExpWithAnd(t *testing.T) {
	t.Parallel()

	tests := []testCase{
		{
			rule: `x eq 1 or y gt 1`,
			input: obj{
				"x": 1,
				"y": 0,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: `x eq 1 and y gt 1`,
			input: obj{
				"x": 1,
				"y": 2,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: `x eq 1 and y gt 1`,
			input: obj{
				"x": 1,
				"y": 1,
			},
			result:   false,
			hasError: false,
		},
		{
			rule: `x eq 1 and not (y gt 1)`,
			input: obj{
				"x": 1,
				"y": 1,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: `x eq 1 and not (y gt 1)`,
			input: obj{
				"x": 1,
				"y": 2,
			},
			result:   false,
			hasError: false,
		},
		{
			rule: `(x eq 1 and y gt 1) and z eq 3`,
			input: obj{
				"x": 1,
				"y": 2,
				"z": 3,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: `(x eq 1 and y gt 1) and z eq 3 or a gt 4`,
			input: obj{
				"x": 1,
				"y": 2,
				"a": 5,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: `(x eq 1 and y gt 1) and (z eq 3 or a gt 4)`,
			input: obj{
				"x": 1,
				"y": 2,
				"a": 5,
			},
			result:   true,
			hasError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.rule, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.result, Evaluate(tt.rule, tt.input), tt.rule)
			assert.Equal(t, tt.result, Evaluate(fmt.Sprintf(parenthesizedFormat, tt.rule), tt.input), tt.rule)
		})
	}
}

func TestLogicalExpWithAndWithExtraSpaces(t *testing.T) {
	t.Parallel()

	tests := []testCase{
		{
			rule: x2AndY,
			input: obj{
				"x": 1,
				"y": 3,
				"z": true,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: x2AndY,
			input: obj{
				"x": 1,
				"y": 3,
			},
			result:   false,
			hasError: false,
		},
		{
			rule: x2AndY,
			input: obj{
				"x": 1,
				"y": 5,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: `x < 2 and ( y > 4 or z == true)`,
			input: obj{
				"x": 1,
				"y": 3,
				"z": true,
			},
			result:   true,
			hasError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.rule, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.result, Evaluate(tt.rule, tt.input), tt.rule)
			assert.Equal(t, tt.result, Evaluate(fmt.Sprintf(parenthesizedFormat, tt.rule), tt.input), tt.rule)
		})
	}
}

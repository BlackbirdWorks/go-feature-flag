package parser

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFloatOperationOperandErrors pins how operand type errors surface per operator. A mistyped operand
// makes the comparison false for every operator, and the evaluator records a debug error for it
// (Evaluator.LastDebugErr). FloatOperation.LE alone returns no error, so no debug error is recorded; the
// cases marked "quirk" are what a correct LE would report. Results are identical, only the debug error
// differs (nothing in the repository reads LastDebugErr).
func TestFloatOperationOperandErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		rule     string
		items    map[string]any
		want     bool
		wantErr  bool
		wantDebg bool
	}{
		{
			name:     "float ge on a string reports the error",
			rule:     `x ge 1.5`,
			items:    map[string]any{"x": "abc"},
			wantErr:  false,
			wantDebg: true,
		},
		{
			name:     "int le on a string reports the error",
			rule:     `x le 2`,
			items:    map[string]any{"x": "abc"},
			wantDebg: true,
		},
		{name: "quirk: float le on a string reports nothing", rule: `x le 1.5`, items: map[string]any{"x": "abc"}},
		{
			name:  "quirk: not float le on a string is true, no debug error",
			rule:  `not (x le 1.5)`,
			items: map[string]any{"x": "abc"},
			want:  true,
		},
		{
			name:     "not float ge on a string is true, with a debug error",
			rule:     `not (x ge 1.5)`,
			items:    map[string]any{"x": "abc"},
			want:     true,
			wantDebg: true,
		},
		{name: "float le on a number", rule: `x le 1.5`, items: map[string]any{"x": 1.0}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ev, err := NewEvaluator(tt.rule)
			require.NoError(t, err)
			got, err := ev.Process(tt.items)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantDebg, ev.LastDebugErr() != nil, "debug error")
		})
	}
}

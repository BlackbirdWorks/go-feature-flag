package parser

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

// TestFloatOperationOperandErrors pins how operand type errors surface per operator. A mistyped operand
// makes the comparison false for every operator, and the evaluator records a debug error for it
// (Evaluator.LastDebugErr), FloatOperation.LE included (issue #50). Results are identical for every operator.
func TestFloatOperationOperandErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		items    map[string]any
		name     string
		rule     string
		want     bool
		wantErr  bool
		wantDebg bool
	}{
		{
			name:     "float ge on a string reports the error",
			rule:     `x ge 1.5`,
			items:    map[string]any{"x": fixtures.UserKeyAbc},
			wantErr:  false,
			wantDebg: true,
		},
		{
			name:     "int le on a string reports the error",
			rule:     `x le 2`,
			items:    map[string]any{"x": fixtures.UserKeyAbc},
			wantDebg: true,
		},
		{
			name:     "float le on a string reports the error",
			rule:     `x le 1.5`,
			items:    map[string]any{"x": fixtures.UserKeyAbc},
			wantDebg: true,
		},
		{
			name:     "not float le on a string is true, with a debug error",
			rule:     `not (x le 1.5)`,
			items:    map[string]any{"x": fixtures.UserKeyAbc},
			want:     true,
			wantDebg: true,
		},
		{
			name:     "not float ge on a string is true, with a debug error",
			rule:     `not (x ge 1.5)`,
			items:    map[string]any{"x": fixtures.UserKeyAbc},
			want:     true,
			wantDebg: true,
		},
		{name: "float le on a number", rule: `x le 1.5`, items: map[string]any{"x": 1.0}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			eval, err := NewEvaluator(tt.rule)
			require.NoError(t, err)

			got, err := eval.Process(tt.items)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantDebg, eval.LastDebugErr() != nil, debugError)
		})
	}
}

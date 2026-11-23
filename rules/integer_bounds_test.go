package rules

import (
	"math"
	"math/big"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// plusOne / minusOne return the decimal string of n+1 / n-1 without any overflow.
func plusOne(n int) string  { return new(big.Int).Add(big.NewInt(int64(n)), big.NewInt(1)).String() }
func minusOne(n int) string { return new(big.Int).Sub(big.NewInt(int64(n)), big.NewInt(1)).String() }

// TestEvaluateIntegerLiteralBounds checks that an integer literal is either evaluated with its exact
// value or rejected with an error: it must never silently wrap around into another integer.
func TestEvaluateIntegerLiteralBounds(t *testing.T) {
	t.Parallel()

	maxInt, minInt := strconv.Itoa(math.MaxInt), strconv.Itoa(math.MinInt)
	hugeDigits := "9"
	for i := 0; i < 40; i++ {
		hugeDigits += "9"
	}

	tests := []struct {
		name    string
		rule    string
		value   any
		want    bool
		wantErr bool
	}{
		// single literals
		{name: "zero", rule: `x eq 0`, value: 0, want: true},
		{name: "negative zero", rule: `x eq -0`, value: 0, want: true},
		{name: "small negative", rule: `x eq -42`, value: -42, want: true},
		{name: "max int", rule: `x eq ` + maxInt, value: math.MaxInt, want: true},
		{name: "min int", rule: `x eq ` + minInt, value: math.MinInt, want: true},
		{name: "max int is not min int", rule: `x eq ` + maxInt, value: math.MinInt, want: false},
		{name: "max int gt", rule: `x gt ` + minInt, value: math.MaxInt, want: true},
		{name: "max int + 1", rule: `x eq ` + plusOne(math.MaxInt), value: math.MinInt, wantErr: true},
		{name: "min int - 1", rule: `x eq ` + minusOne(math.MinInt), value: math.MaxInt, wantErr: true},
		{name: "uint64 max does not wrap to -1", rule: `x eq 18446744073709551615`, value: -1, wantErr: true},
		{name: "2^32 does not wrap to 0", rule: `x eq 4294967296`, value: 0, want: false},
		{name: "huge digit string", rule: `x eq ` + hugeDigits, value: 0, wantErr: true},
		{name: "huge negative digit string", rule: `x eq -` + hugeDigits, value: 0, wantErr: true},
		{name: "huge literal after a valid or", rule: `x eq 1 or x eq ` + hugeDigits, value: 5, wantErr: true},

		// lists
		{name: "list with max int", rule: `x in [0, ` + maxInt + `]`, value: math.MaxInt, want: true},
		{name: "list with zero", rule: `x in [0, ` + maxInt + `]`, value: 0, want: true},
		{name: "list miss", rule: `x in [0, ` + maxInt + `]`, value: math.MinInt, want: false},
		// the grammar does not accept negative integers in a list (INT is unsigned): syntax error, not a wrap.
		{name: "list with min int is a syntax error", rule: `x in [` + minInt + `, 1]`, value: 1, wantErr: true},
		{name: "list with max int + 1", rule: `x in [1, ` + plusOne(math.MaxInt) + `]`, value: 1, wantErr: true},
		{name: "list with huge first element", rule: `x in [` + hugeDigits + `, 1]`, value: 1, wantErr: true},
		{name: "list with uint64 max does not wrap to -1", rule: `x in [18446744073709551615]`, value: -1, wantErr: true},
		{name: "list with 2^32 does not wrap to 0", rule: `x in [4294967296, 5]`, value: 0, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := Evaluate(tt.rule, map[string]any{"x": tt.value})
			if tt.wantErr {
				require.Error(t, err)
				assert.False(t, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

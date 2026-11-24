package parser

import (
	"math"
	"math/big"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func decimalPlusOne(n int) string { return new(big.Int).Add(big.NewInt(int64(n)), big.NewInt(1)).String() }
func decimalMinusOne(n int) string {
	return new(big.Int).Sub(big.NewInt(int64(n)), big.NewInt(1)).String()
}

// TestIntegerLiteralBoundsBothImplementations runs the integer literals around the bounds of int through
// the compiled evaluator and through the original ANTLR visitor: both must return the exact result or an
// error, and never wrap the literal around.
func TestIntegerLiteralBoundsBothImplementations(t *testing.T) {
	t.Parallel()
	maxInt, minInt := strconv.Itoa(math.MaxInt), strconv.Itoa(math.MinInt)

	tests := []struct {
		name    string
		rule    string
		value   int
		want    bool
		wantErr bool
	}{
		{name: "max int", rule: `x eq ` + maxInt, value: math.MaxInt, want: true},
		{name: "min int", rule: `x eq ` + minInt, value: math.MinInt, want: true},
		{name: "max int + 1", rule: `x eq ` + decimalPlusOne(math.MaxInt), value: math.MinInt, wantErr: true},
		{name: "min int - 1", rule: `x eq ` + decimalMinusOne(math.MinInt), value: math.MaxInt, wantErr: true},
		{name: "uint64 max does not wrap to -1", rule: `x eq 18446744073709551615`, value: -1, wantErr: true},
		{name: "2^32 does not wrap to 0", rule: `x eq 4294967296`, value: 0, want: false},
		{name: "list with max int", rule: `x in [1, ` + maxInt + `]`, value: math.MaxInt, want: true},
		{name: "list with max int + 1", rule: `x in [1, ` + decimalPlusOne(math.MaxInt) + `]`, value: 1, wantErr: true},
		{name: "list with uint64 max does not wrap to -1", rule: `x in [18446744073709551615]`, value: -1, wantErr: true},
		{name: "list with 2^32 does not wrap to 0", rule: `x in [4294967296, 5]`, value: 0, want: false},
		{name: "huge list element", rule: `x in [1, 99999999999999999999999999999999999999]`, value: 1, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			items := map[string]interface{}{"x": tt.value}

			ev, err := NewEvaluator(tt.rule)
			require.NoError(t, err)
			compiled, compiledErr := ev.Process(items)
			tree, err := parseRule(tt.rule)
			require.NoError(t, err)
			visited, visitedErr, _ := visitorProcess(tree, items)

			if tt.wantErr {
				assert.Error(t, compiledErr, "compiled")
				assert.Error(t, visitedErr, "visitor")
				assert.False(t, compiled)
				assert.False(t, visited)
				return
			}
			assert.NoError(t, compiledErr, "compiled")
			assert.NoError(t, visitedErr, "visitor")
			assert.Equal(t, tt.want, compiled, "compiled")
			assert.Equal(t, tt.want, visited, "visitor")
		})
	}
}

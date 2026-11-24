package rules

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvaluate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		rule    string
		items   map[string]any
		want    bool
		wantErr bool
	}{
		{name: "string equal", rule: `x eq "abc"`, items: map[string]any{"x": "abc"}, want: true},
		{name: "string not equal", rule: `x eq "abc"`, items: map[string]any{"x": "abd"}, want: false},
		{name: "unquoted string is a syntax error", rule: `x eq abc`, items: map[string]any{"x": "abc"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := Evaluate(tt.rule, tt.items)
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

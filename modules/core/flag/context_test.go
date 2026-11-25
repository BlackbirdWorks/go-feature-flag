package flag_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/modules/core/flag"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

func TestContext_AddIntoEvaluationContextEnrichment(t *testing.T) {
	t.Parallel()

	type args struct {
		value any
		key   string
	}

	tests := []struct {
		args                        args
		expected                    any
		EvaluationContextEnrichment map[string]any
		name                        string
	}{
		{
			name:                        "Add a new key to a nil map",
			EvaluationContextEnrichment: nil,
			args: args{
				key:   fixtures.Env,
				value: fixtures.Prod,
			},
			expected: fixtures.Prod,
		},
		{
			name:                        "Add a new key to an existing map",
			EvaluationContextEnrichment: map[string]any{"john": "doe"},
			args: args{
				key:   fixtures.Env,
				value: fixtures.Prod,
			},
			expected: fixtures.Prod,
		},
		{
			name:                        "Override an existing key",
			EvaluationContextEnrichment: map[string]any{fixtures.Env: "dev"},
			args: args{
				key:   fixtures.Env,
				value: fixtures.Prod,
			},
			expected: fixtures.Prod,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := &flag.Context{Service: flag.NewService(),
				EvaluationContextEnrichment: tt.EvaluationContextEnrichment,
			}
			s.AddIntoEvaluationContextEnrichment(tt.args.key, tt.args.value)
			assert.Equal(t, tt.expected, s.EvaluationContextEnrichment[tt.args.key])
		})
	}
}

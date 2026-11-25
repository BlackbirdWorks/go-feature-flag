package flagdef

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

const (
	bar      = "bar"
	foo      = "foo"
	testRule = "test-rule"
)

func Test_constructMetadataParallel(t *testing.T) {
	t.Parallel()

	sharedMetadata := map[string]any{
		"key1": "value1",
	}

	tests := make([]struct {
		name                  string
		wantEvaluatedRuleName string
	}, 0, 10_000)

	// generate test cases
	for i := range 10_000 {
		ruleName := fmt.Sprintf("rule-%d", i)
		tests = append(tests, struct {
			name                  string
			wantEvaluatedRuleName string
		}{
			name:                  fmt.Sprintf("Rule %d", i),
			wantEvaluatedRuleName: ruleName,
		})
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := constructMetadata(sharedMetadata, &tt.wantEvaluatedRuleName)
			assert.Equal(t, tt.wantEvaluatedRuleName, got[evaluatedRuleNameKey])
		})
	}
}

func Test_ConstructMetadata(t *testing.T) {
	t.Parallel()

	rule := testRule

	tests := []struct {
		flagMetadata map[string]any
		ruleName     *string
		want         map[string]any
		name         string
	}{
		{
			name:         "nil metadata, nil rule",
			flagMetadata: nil,
			ruleName:     nil,
			want:         nil,
		},
		{
			name:         "nil metadata, empty rule",
			flagMetadata: nil,
			ruleName:     new(""),
			want:         nil,
		},
		{
			name:         "nil metadata, non-empty rule",
			flagMetadata: nil,
			ruleName:     &rule,
			want:         map[string]any{evaluatedRuleNameKey: testRule},
		},
		{
			name:         "non-nil metadata, nil rule",
			flagMetadata: map[string]any{foo: bar},
			ruleName:     nil,
			want:         map[string]any{foo: bar},
		},
		{
			name:         "non-nil metadata, empty rule",
			flagMetadata: map[string]any{foo: bar},
			ruleName:     new(""),
			want:         map[string]any{foo: bar},
		},
		{
			name:         "non-nil metadata, non-empty rule",
			flagMetadata: map[string]any{foo: bar},
			ruleName:     &rule,
			want:         map[string]any{foo: bar, evaluatedRuleNameKey: testRule},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := constructMetadata(tt.flagMetadata, tt.ruleName)
			assert.Equal(t, tt.want, got)
		})
	}
}

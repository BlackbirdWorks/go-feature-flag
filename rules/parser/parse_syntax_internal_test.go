package parser

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	env     = "env"
	pro     = "pro"
	xEq1And = "x eq 1  and y eq 2"
	xEqAB   = `x eq "a  b"`
)

func TestIssue41RuleValidator(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		rule    string
		wantErr bool
	}{
		{
			name:    "valid rule",
			rule:    `env eq "pro"`,
			wantErr: false,
		},
		{
			name:    "missing closing parenthesis",
			rule:    `((env eq "pro") and (company eq "my-company")`,
			wantErr: true,
		},
		{
			name:    "invalid extra operator",
			rule:    `env eq "pro" eq "dev"`,
			wantErr: true,
		},
		{
			name:    "no operator",
			rule:    `invalid`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			eval, err := NewEvaluator(tt.rule)
			if tt.wantErr {
				require.Error(t, err, tt.rule)
				assert.Nil(t, eval)

				return
			}

			require.NoError(t, err, tt.rule)
			assert.NotNil(t, eval)
		})
	}
}

func TestNewEvaluatorRejectsInvalidSyntax(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		rule string
	}{
		{name: "missing closing parenthesis", rule: `((env eq "pro") and (company eq "my-company")`},
		{name: "extra operator", rule: `env eq "pro" eq "dev"`},
		{name: "no operator", rule: `invalid`},
		{name: "leftover tokens", rule: `x eq 1 leftover`},
		{name: "extra closing parenthesis", rule: `(env eq "pro"))`},
		{name: "empty rule", rule: ``},
		{name: "unclosed string", rule: `x eq "abc`},
		{name: "incomplete and", rule: `x eq 1 and`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ev, err := NewEvaluator(tt.rule)
			require.Error(t, err, tt.rule)
			assert.Nil(t, ev)
		})
	}
}

func TestNewEvaluatorWhitespacePolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input obj
		name  string
		rule  string
		want  bool
	}{
		{
			name:  "leading space",
			rule:  ` env eq "pro"`,
			input: obj{env: pro},
			want:  true,
		},
		{
			name:  "trailing CRLF",
			rule:  "env eq \"pro\"\r\n",
			input: obj{env: pro},
			want:  true,
		},
		{
			name:  "CRLF only",
			rule:  "env eq \"pro\"\r",
			input: obj{env: pro},
			want:  true,
		},
		{
			name:  "double space before and evaluates both sides",
			rule:  xEq1And,
			input: obj{"x": 1, "y": 2},
			want:  true,
		},
		{
			name:  "double space before and is not a prefix of the left clause",
			rule:  xEq1And,
			input: obj{"x": 1, "y": 99},
			want:  false,
		},
		{
			name:  "extra spaces around operator",
			rule:  `x eq  1`,
			input: obj{"x": 1},
			want:  true,
		},
		{
			name:  "spaces inside string are preserved",
			rule:  xEqAB,
			input: obj{"x": "a  b"},
			want:  true,
		},
		{
			name:  "trailing spaces and newline",
			rule:  "env eq \"pro\"  \n",
			input: obj{env: pro},
			want:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ev, err := NewEvaluator(tt.rule)
			require.NoError(t, err, tt.rule)
			result, err := ev.Process(tt.input)
			require.NoError(t, err, tt.rule)
			assert.Equal(t, tt.want, result, tt.rule)
		})
	}
}

func TestNormalizeRuleInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		rule string
		want string
	}{
		{name: "collapses double spaces", rule: xEq1And, want: `x eq 1 and y eq 2`},
		{name: "trims space and CRLF", rule: " env eq \"pro\" \r\n", want: `env eq "pro"`},
		{name: "keeps spaces inside strings", rule: xEqAB, want: xEqAB},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, normalizeRuleInput(tt.rule))
		})
	}
}

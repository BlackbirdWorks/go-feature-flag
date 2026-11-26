package parser_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/rules/parser"
)

const (
	attrKey          = "key"
	attrTargetingKey = "targetingKey"
	attrEmail        = "email"
	attrAge          = "age"
	attrSegment      = "segment"
	errBadName       = "invalid segment name"
	errNotLiteral    = "must be a string literal"
	errNotAfterIn    = `only valid as the right side of "in"`
	ruleSegmentX     = `segment eq "x"`
	ruleSegmentS     = `targetingKey in segment("s")`
	ruleSegmentNotS  = `not (targetingKey in segment("s"))`
	ruleSegmentAorB  = `targetingKey in segment("a") or email in segment("b")`
)

func TestSegmentRuleParsing(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("a", parser.MaxSegmentNameLen)

	tests := []struct {
		name      string
		rule      string
		wantErr   string // substring of the error, empty for a valid rule
		wantTerms []parser.SegmentTerm
	}{
		{
			name:      "simple",
			rule:      `targetingKey in segment("beta-users")`,
			wantTerms: []parser.SegmentTerm{{Name: "beta-users", Path: []string{attrTargetingKey}}},
		},
		{
			name:      "upper case IN",
			rule:      `targetingKey IN segment("a")`,
			wantTerms: []parser.SegmentTerm{{Name: "a", Path: []string{attrTargetingKey}}},
		},
		{
			name:      "nested attribute",
			rule:      `user.email in segment("a")`,
			wantTerms: []parser.SegmentTerm{{Name: "a", Path: []string{"user", attrEmail}}},
		},
		{
			name:      "blanks inside the call",
			rule:      "key in segment (  \"a\"\t)",
			wantTerms: []parser.SegmentTerm{{Name: "a", Path: []string{attrKey}}},
		},
		{
			name:      "blank lines around",
			rule:      "\n  key in segment(\"a\")  \n",
			wantTerms: []parser.SegmentTerm{{Name: "a", Path: []string{attrKey}}},
		},
		{
			name: "and or not parenthesis",
			rule: `targetingKey in segment("a") and not (email in segment("b")) or (x eq 1 and key in segment("c"))`,
			wantTerms: []parser.SegmentTerm{
				{Name: "a", Path: []string{attrTargetingKey}},
				{Name: "b", Path: []string{attrEmail}},
				{Name: "c", Path: []string{attrKey}},
			},
		},
		{
			name:      "the same term twice is one term",
			rule:      `key in segment("a") or key in segment("a")`,
			wantTerms: []parser.SegmentTerm{{Name: "a", Path: []string{attrKey}}},
		},
		{
			name: "same segment for two attributes is two terms",
			rule: `key in segment("a") or email in segment("a")`,
			wantTerms: []parser.SegmentTerm{
				{Name: "a", Path: []string{attrKey}},
				{Name: "a", Path: []string{attrEmail}},
			},
		},
		{
			name:      "longest name",
			rule:      `key in segment("` + long + `")`,
			wantTerms: []parser.SegmentTerm{{Name: long, Path: []string{attrKey}}},
		},
		{
			name:      "name charset",
			rule:      `key in segment("a_b-c9")`,
			wantTerms: []parser.SegmentTerm{{Name: "a_b-c9", Path: []string{attrKey}}},
		},
		{
			name:      "unicode in another string of the rule",
			rule:      `name eq "é" and key in segment("a")`,
			wantTerms: []parser.SegmentTerm{{Name: "a", Path: []string{attrKey}}},
		},
		{name: "name too long", rule: `key in segment("` + long + `a")`, wantErr: errBadName},
		{name: "empty name", rule: `key in segment("")`, wantErr: errBadName},
		{name: "upper case name", rule: `key in segment("Beta")`, wantErr: errBadName},
		{name: "unicode name", rule: `key in segment("bétà")`, wantErr: errBadName},
		{name: "space in name", rule: `key in segment("a b")`, wantErr: errBadName},
		{name: "escape in name", rule: `key in segment("a\"b")`, wantErr: errBadName},
		{name: "name is not a literal", rule: `key in segment(beta)`, wantErr: errNotLiteral},
		{name: "number name", rule: `key in segment(1)`, wantErr: errNotLiteral},
		{name: "no argument", rule: `key in segment()`, wantErr: errNotLiteral},
		{name: "two arguments", rule: `key in segment("a", "b")`, wantErr: "exactly one argument"},
		{name: "unclosed call", rule: `key in segment("a"`, wantErr: "exactly one argument"},
		{name: "unterminated name", rule: `key in segment("a`, wantErr: "unterminated segment name"},
		{name: "not after in", rule: `key eq segment("a")`, wantErr: errNotAfterIn},
		{name: "call alone", rule: `segment("a")`, wantErr: errNotAfterIn},
		{name: "left side", rule: `segment("a") in key`, wantErr: errNotAfterIn},
		{name: "in list", rule: `key in [segment("a")]`, wantErr: errNotAfterIn},
		{name: "marker byte", rule: "key in segment(\"a\") and x eq \"\x01\"", wantErr: "control characters"},
		{name: "pad byte", rule: "key in segment(\"a\") and x eq \"\x02\"", wantErr: "control characters"},
		{name: "no argument end", rule: `key in segment(`, wantErr: errNotLiteral},
		{name: "syntax error elsewhere", rule: `key in segment("a") and`, wantErr: "invalid rule"},

		// the word segment as an attribute or inside a string is not the function
		{name: "attribute named segment", rule: ruleSegmentX},
		{name: "attribute segment in list", rule: `segment in ["a", "b"]`},
		{name: "nested attribute segment", rule: `a.segment eq "x"`},
		{name: "attribute ending in segment", rule: `mysegment eq "x"`},
		{
			name: "attribute segment then call",
			rule: `segment eq "x" and key in segment("a")`,
			wantTerms: []parser.SegmentTerm{
				{Name: "a", Path: []string{attrKey}},
			},
		},
		{name: "function text in a string", rule: `name eq "key in segment(\"a\")"`},
		{name: "function text in a string, unbalanced", rule: `name eq "segment(("`},
		{name: "a plain rule", rule: `age gt 18`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			evaluator, err := parser.NewEvaluator(tt.rule)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantTerms, evaluator.SegmentTerms())
			assert.Equal(t, len(tt.wantTerms) > 0, evaluator.HasSegments())
		})
	}
}

func TestSegmentErrorsCarryTheOffsetAndTheSentinel(t *testing.T) {
	t.Parallel()

	rule := `age gt 18 and key in segment("Bad")`

	_, err := parser.NewEvaluator(rule)
	require.ErrorIs(t, err, parser.ErrSegmentSyntax)

	var segErr *parser.SegmentError

	require.ErrorAs(t, err, &segErr)
	assert.Equal(t, strings.Index(rule, `Bad`), segErr.Offset)
}

func TestSegmentRuleTooManyTerms(t *testing.T) {
	t.Parallel()

	terms := make([]string, 0, parser.MaxSegmentTerms+1)
	for i := range parser.MaxSegmentTerms + 1 {
		terms = append(terms, `key in segment("s`+strings.Repeat("a", i)+`")`)
	}

	_, err := parser.NewEvaluator(strings.Join(terms, " or "))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "too many")

	_, err = parser.NewEvaluator(strings.Join(terms[:parser.MaxSegmentTerms], " or "))
	require.NoError(t, err)
}

func TestSegmentSyntaxErrorOfTheRestOfTheRule(t *testing.T) {
	t.Parallel()

	// the call is replaced by a literal of the same length, so the column of a later error does not move
	_, err := parser.NewEvaluator(`key in segment("beta") and age gt`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "line 1:33", "the end of the rule is at the column it has in the rule as written")
}

func TestProcessSegmentsThreeValued(t *testing.T) {
	t.Parallel()

	const (
		member    = parser.SegmentMember
		notMember = parser.SegmentNotMember
		unknown   = parser.SegmentUnknown
	)

	items := map[string]any{attrTargetingKey: "k", attrEmail: "e@x", attrAge: 30, "plan": "pro"}

	tests := []struct {
		name   string
		rule   string
		states []parser.SegmentState
		want   parser.Match
	}{
		{name: "member", rule: ruleSegmentS, states: []parser.SegmentState{member}, want: parser.MatchYes},
		{
			name:   "not member",
			rule:   `targetingKey in segment("s")`,
			states: []parser.SegmentState{notMember},
			want:   parser.MatchNo,
		},
		{
			name:   "unknown",
			rule:   `targetingKey in segment("s")`,
			states: []parser.SegmentState{unknown},
			want:   parser.MatchUnknown,
		},
		{
			name:   "exclude: not member",
			rule:   ruleSegmentNotS,
			states: []parser.SegmentState{notMember},
			want:   parser.MatchYes,
		},
		{
			name:   "exclude: member",
			rule:   ruleSegmentNotS,
			states: []parser.SegmentState{member},
			want:   parser.MatchNo,
		},
		{
			name:   "exclude: unknown",
			rule:   ruleSegmentNotS,
			states: []parser.SegmentState{unknown},
			want:   parser.MatchUnknown,
		},
		{
			name: "include and exclude", rule: `targetingKey in segment("in") and not (email in segment("out"))`,
			states: []parser.SegmentState{member, notMember}, want: parser.MatchYes,
		},
		{
			name:   "include and exclude, excluded",
			rule:   `targetingKey in segment("in") and not (email in segment("out"))`,
			states: []parser.SegmentState{member, member},
			want:   parser.MatchNo,
		},
		{
			name: "mixed with an attribute, true", rule: `plan eq "pro" and targetingKey in segment("s")`,
			states: []parser.SegmentState{member}, want: parser.MatchYes,
		},
		{
			name:   "mixed with an attribute, false is independent of unknown",
			rule:   `plan eq "free" and targetingKey in segment("s")`,
			states: []parser.SegmentState{unknown},
			want:   parser.MatchNo,
		},
		{
			name:   "mixed with an attribute, true depends on unknown",
			rule:   `plan eq "pro" and targetingKey in segment("s")`,
			states: []parser.SegmentState{unknown},
			want:   parser.MatchUnknown,
		},
		{
			name: "or, independent: true or unknown", rule: `plan eq "pro" or targetingKey in segment("s")`,
			states: []parser.SegmentState{unknown}, want: parser.MatchYes,
		},
		{
			name: "or, dependent: false or unknown", rule: `plan eq "free" or targetingKey in segment("s")`,
			states: []parser.SegmentState{unknown}, want: parser.MatchUnknown,
		},
		{
			name: "or of two segments, one member", rule: ruleSegmentAorB,
			states: []parser.SegmentState{unknown, member}, want: parser.MatchYes,
		},
		{
			name: "or of two segments, none member", rule: ruleSegmentAorB,
			states: []parser.SegmentState{notMember, notMember}, want: parser.MatchNo,
		},
		{
			name: "or of two segments, one unknown", rule: ruleSegmentAorB,
			states: []parser.SegmentState{notMember, unknown}, want: parser.MatchUnknown,
		},
		{
			name: "and of two segments, one not member", rule: `targetingKey in segment("a") and email in segment("b")`,
			states: []parser.SegmentState{unknown, notMember}, want: parser.MatchNo,
		},
		{
			name: "not of an or with unknown but decided", rule: `not (plan eq "pro" or targetingKey in segment("s"))`,
			states: []parser.SegmentState{unknown}, want: parser.MatchNo,
		},
		{
			name:   "same term used twice reads the same state",
			rule:   `targetingKey in segment("s") or not (targetingKey in segment("s"))`,
			states: []parser.SegmentState{unknown},
			want:   parser.MatchUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			evaluator, err := parser.NewEvaluator(tt.rule)
			require.NoError(t, err)

			got, err := evaluator.ProcessSegments(items, tt.states)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestProcessSegmentsErrors(t *testing.T) {
	t.Parallel()

	evaluator, err := parser.NewEvaluator(`targetingKey in segment("s") and age gt 3`)
	require.NoError(t, err)

	t.Run("wrong number of states", func(t *testing.T) {
		t.Parallel()

		_, procErr := evaluator.ProcessSegments(map[string]any{attrAge: 5}, nil)
		require.ErrorIs(t, procErr, parser.ErrSegmentStates)
	})

	t.Run("Process refuses a rule with segment terms", func(t *testing.T) {
		t.Parallel()

		ok, procErr := evaluator.Process(map[string]any{attrAge: 5})
		require.ErrorIs(t, procErr, parser.ErrSegmentsNotResolved)
		assert.False(t, ok)
	})
}

func TestProcessSegmentsReportsTheErrorOfAnotherTerm(t *testing.T) {
	t.Parallel()

	evaluator, err := parser.NewEvaluator(`plan gt true and targetingKey in segment("s")`)
	require.NoError(t, err)

	got, err := evaluator.ProcessSegments(map[string]any{"plan": "pro"}, []parser.SegmentState{parser.SegmentMember})
	require.Error(t, err)
	assert.Equal(t, parser.MatchUnknown, got)
	require.Error(t, evaluator.LastDebugErr(), "the debug error of the term is kept")
}

func TestRulesWithoutSegmentsAreUnchanged(t *testing.T) {
	t.Parallel()

	tests := []struct {
		items map[string]any
		name  string
		rule  string
		want  bool
	}{
		{name: "attribute named segment", rule: ruleSegmentX, items: map[string]any{attrSegment: "x"}, want: true},
		{
			name:  "attribute named segment, other value",
			rule:  `segment eq "x"`,
			items: map[string]any{attrSegment: "y"},
			want:  false,
		},
		{name: "list on segment", rule: `segment in ["a","b"]`, items: map[string]any{attrSegment: "b"}, want: true},
		{
			name:  "marker-like text in a string",
			rule:  `name eq "segment(a)"`,
			items: map[string]any{"name": `segment(a)`},
			want:  true,
		},
		{
			name:  "control character without a call",
			rule:  "name eq \"\x01a\"",
			items: map[string]any{"name": "\x01a"},
			want:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			evaluator, err := parser.NewEvaluator(tt.rule)
			require.NoError(t, err)
			assert.False(t, evaluator.HasSegments())

			got, err := evaluator.Process(tt.items)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestValidSegmentName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want bool
	}{
		{name: "letters", in: "abc", want: true},
		{name: "all the classes", in: "a-b_c0", want: true},
		{name: "one char", in: "a", want: true},
		{name: "64 chars", in: strings.Repeat("a", 64), want: true},
		{name: "65 chars", in: strings.Repeat("a", 65), want: false},
		{name: "empty", in: "", want: false},
		{name: "upper", in: "A", want: false},
		{name: "dot", in: "a.b", want: false},
		{name: "slash", in: "a/b", want: false},
		{name: "unicode", in: "é", want: false},
		{name: "nul", in: "a\x00", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, parser.ValidSegmentName(tt.in))
		})
	}
}

func TestSegmentEnumsPrint(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "member", parser.SegmentMember.String())
	assert.Equal(t, "notMember", parser.SegmentNotMember.String())
	assert.Equal(t, "unknown", parser.SegmentUnknown.String())
	assert.Equal(t, "SegmentState(9)", parser.SegmentState(9).String())
	assert.Equal(t, "yes", parser.MatchYes.String())
	assert.Equal(t, "no", parser.MatchNo.String())
	assert.Equal(t, "unknown", parser.MatchUnknown.String())
	assert.Equal(t, "Match(9)", parser.Match(9).String())
}

func FuzzSegmentRule(f *testing.F) {
	for _, seed := range []string{
		`targetingKey in segment("a")`,
		`not (a in segment("b")) and c eq 1`,
		`segment eq "x"`,
		`key in segment ( "a" ) or key in segment("a")`,
		"key in segment(\"a\"\x01)",
		`key in segment("`,
		`segment(segment("a"))`,
		`x in segment("é")`,
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, rule string) {
		evaluator, err := parser.NewEvaluator(rule)
		if err != nil || !evaluator.HasSegments() {
			return
		}

		terms := evaluator.SegmentTerms()
		require.NotEmpty(t, terms)
		require.LessOrEqual(t, len(terms), parser.MaxSegmentTerms)

		for _, term := range terms {
			require.True(t, parser.ValidSegmentName(term.Name), "term %+v", term)
			require.NotEmpty(t, term.Path, "term %+v", term)
		}

		// compile -> evaluate with every state is stable and never panics
		for _, state := range []parser.SegmentState{parser.SegmentUnknown, parser.SegmentNotMember, parser.SegmentMember} {
			states := make([]parser.SegmentState, len(terms))
			for i := range states {
				states[i] = state
			}

			first, _ := evaluator.ProcessSegments(map[string]any{}, states)
			second, _ := evaluator.ProcessSegments(map[string]any{}, states)

			require.Equal(t, first, second, "evaluation is not stable")
		}
	})
}

func TestMatchHelpers(t *testing.T) {
	t.Parallel()

	assert.Equal(t, parser.MatchNo, parser.MatchYes.Not())
	assert.Equal(t, parser.MatchYes, parser.MatchNo.Not())
	assert.Equal(t, parser.MatchUnknown, parser.MatchUnknown.Not())
	assert.Equal(t, parser.MatchUnknown, parser.Match(9).Not())

	assert.Equal(t, parser.MatchYes, parser.SegmentMember.Match())
	assert.Equal(t, parser.MatchNo, parser.SegmentNotMember.Match())
	assert.Equal(t, parser.MatchUnknown, parser.SegmentUnknown.Match())
	assert.Equal(t, parser.MatchUnknown, parser.SegmentState(9).Match())
}

package parser

import (
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/antlr4-go/antlr/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The rewrite of the segment() calls is a text transformation in front of the generated lexer. These tests are its
// safety argument: scanSegments is compared with the token stream that the real lexer reads.

type lexedToken struct {
	text       string
	start, end int // byte offsets, end excluded
	kind       int
}

// lexAll returns the tokens of text and whether the lexer reported no error. Positions are in bytes.
func lexAll(text string) ([]lexedToken, bool) {
	byteOf := make([]int, 0, len(text)+1)
	for i := range text {
		byteOf = append(byteOf, i)
	}

	byteOf = append(byteOf, len(text))

	listener := &collectingErrorListener{DefaultErrorListener: antlr.NewDefaultErrorListener()}
	lexer := NewJsonQueryLexer(antlr.NewInputStream(text))
	lexer.RemoveErrorListeners()
	lexer.AddErrorListener(listener)

	all := lexer.GetAllTokens()
	tokens := make([]lexedToken, 0, len(all))

	for _, tok := range all {
		tokens = append(tokens, lexedToken{
			text: tok.GetText(), kind: tok.GetTokenType(),
			start: byteOf[tok.GetStart()], end: byteOf[tok.GetStop()+1],
		})
	}

	return tokens, len(listener.errs) == 0
}

func isBlank(kind int) bool { return kind == JsonQueryLexerSP || kind == JsonQueryLexerNEWLINE }

// callSpans returns the span (byte offsets) of every call of segment() that the lexer sees: an ATTRNAME token
// segment followed by an opening parenthesis, up to the closing one. afterIn is true for the calls that follow the
// IN keyword.
func callSpans(tokens []lexedToken, ruleLen int) ([][2]int, []bool) {
	var (
		spans   [][2]int
		afterIn []bool
	)

	for i, tok := range tokens {
		if tok.kind != JsonQueryLexerATTRNAME || tok.text != segmentKeyword {
			continue
		}

		next := i + 1
		for next < len(tokens) && isBlank(tokens[next].kind) {
			next++
		}

		prev := i - 1
		for prev >= 0 && isBlank(tokens[prev].kind) {
			prev--
		}

		if next >= len(tokens) || tokens[next].kind != JsonQueryLexerT__0 {
			continue
		}

		end := ruleLen

		for _, later := range tokens[next:] {
			if later.kind == JsonQueryLexerT__1 { // ")"
				end = later.end

				break
			}
		}

		spans = append(spans, [2]int{tok.start, end})
		afterIn = append(afterIn, prev >= 0 && tokens[prev].kind == JsonQueryLexerIN)
	}

	return spans, afterIn
}

// checkRewriteShape checks what scanSegments returns without the lexer.
func checkRewriteShape(t *testing.T, rule string) (string, bool, error) {
	t.Helper()

	out, has, err := scanSegments(rule)
	if err == nil && has {
		require.Len(t, out, len(rule), "the rewrite keeps the length of the rule: %q", rule)
	}

	if err == nil && !has {
		require.Equal(t, rule, out, "no call: the rule is returned as it is")
	}

	return out, has, err
}

// checkScannerAgreesWithLexer reports every disagreement between scanSegments and the lexer on rule.
func checkScannerAgreesWithLexer(t *testing.T, rule string) {
	t.Helper()

	if !utf8.ValidString(rule) {
		return
	}

	out, has, err := checkRewriteShape(t, rule)

	tokens, clean := lexAll(rule)
	if !clean {
		return
	}

	spans, afterIn := callSpans(tokens, len(rule))
	for _, isAfterIn := range afterIn {
		if isAfterIn {
			require.True(t, has || err != nil, "a call after in is not missed: %q", rule)
		}
	}

	if err != nil || !has {
		return
	}

	// a byte changes only inside a call, that starts at an ATTRNAME token (never inside a string token)
	for pos := range len(rule) {
		if out[pos] == rule[pos] {
			continue
		}

		inCall := slices.ContainsFunc(spans, func(span [2]int) bool { return pos >= span[0] && pos < span[1] })
		require.True(t, inCall, "byte %d changed outside of a segment call: %q", pos, rule)
	}
}

func scannerAlphabet() []string {
	return []string{
		"segment",
		"SEGMENT",
		"Segment",
		"in",
		"IN",
		"In",
		"(",
		")",
		`"`,
		`\`,
		`\"`,
		`\\`,
		"a",
		"k",
		"x1",
		" ",
		"  ",
		"\n",
		"\r",
		"\t",
		"é",
		"\u65e5",
		"\x01",
		"\x02",
		"eq",
		"ne",
		"1",
		"and",
		"or",
		"not",
		"[",
		"]",
		",",
		".",
		"-",
		"_",
		`"beta"`,
		`segment("a")`,
		`in segment("a")`,
		`k in segment("beta")`,
		`"segment("`,
		`a.segment`,
		"mysegment",
	}
}

func TestScannerAgreesWithLexerOnGeneratedRules(t *testing.T) {
	t.Parallel()

	alphabet := scannerAlphabet()
	rng := rand.New(rand.NewPCG(14, 20))

	for range 30000 {
		var rule strings.Builder

		for range 1 + rng.IntN(14) {
			rule.WriteString(alphabet[rng.IntN(len(alphabet))])
		}

		text := rule.String()

		checkScannerAgreesWithLexer(t, text)
		// the lexer is stricter about blanks than the scanner: compare on the text it is given by the parser too
		normalized := normalizeRuleInput(text)
		checkScannerAgreesWithLexer(t, normalized)

		_, rawHas, rawErr := scanSegments(text)
		_, normHas, normErr := scanSegments(normalized)

		if rawErr == nil && normErr == nil {
			require.Equal(t, rawHas, normHas, "blanks do not change what is a call: %q", text)
		}
	}
}

func FuzzScannerAgreesWithLexer(f *testing.F) {
	for _, seed := range []string{
		`k in segment("a")`, `segment eq "x" and k in segment("b")`, `x eq "segment(" and k IN segment("a")`,
		"k in segment (\r\n\"a\"\t)", `"a\"segment(\"" in segment("b")`, "é in segment(\"a\")", "k in segment(\"a\x01\")",
		`k in segmentx("a")`, `k in a.segment("a")`, `k in segment("a"`,
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, rule string) {
		checkScannerAgreesWithLexer(t, rule)
		checkScannerAgreesWithLexer(t, normalizeRuleInput(rule))
	})
}

func TestScannerSeedsBehave(t *testing.T) {
	t.Parallel()

	out, has, err := scanSegments(`name eq "segment(\"a\")" and k in segment("a")`)
	require.NoError(t, err)
	assert.True(t, has)
	assert.Contains(t, out, `"segment(\"a\")"`, "the call inside a string is left alone")
	assert.NotContains(t, out[strings.LastIndex(out, "and"):], "segment(")
}

func TestSegmentName(t *testing.T) {
	t.Parallel()

	name, ok := segmentName("\x01beta\x02\x02\x02")
	assert.True(t, ok)
	assert.Equal(t, "beta", name)

	_, ok = segmentName("beta")
	assert.False(t, ok, "a string that does not start with the marker is not a segment")

	_, ok = segmentName("")
	assert.False(t, ok)
}

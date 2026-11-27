package flagdef_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

const (
	quotedBracketBreak = "key eq \"ab[\nc\""
	quotedLetterBreak  = "key eq \"ab\nc\""
)

const newlineInString = "{\"==\": [{\"var\": \"key\"}, \"a\nbc\"]}"

const consecutiveOperands = "{\"in\": [{\"var\": \"n\"}, [1\n2]]}"

func multilineFlag(query string, scheduled bool) *flagdef.InternalFlag {
	rule := flagdef.Rule{Query: &query, VariationResult: new("A")}
	flg := &flagdef.InternalFlag{
		Variations:  &map[string]*any{"A": new(any("a")), "B": new(any("b"))},
		DefaultRule: &flagdef.Rule{VariationResult: new("B")},
	}

	if scheduled {
		flg.Scheduled = &[]flagdef.ScheduledStep{{Rules: &[]flagdef.Rule{rule}, Date: new(fixedNow().Add(-time.Hour))}}
	} else {
		flg.Rules = &[]flagdef.Rule{rule}
	}

	return flg
}

func TestInternalFlag_MultilineQueries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantValue any
		name      string
		query     string
		key       string // the evaluation key; fixtures.UserKeyAbc when empty
		n         int
		scheduled bool
		evalOnly  bool // skip load-time validation: a scheduled step is evaluated without it
		wantValid bool
	}{
		{
			name: "jsonlogic operands on consecutive lines are not joined", wantValid: false,
			query: consecutiveOperands,
		},
		{
			name: "jsonlogic multi-line equals its single-line form", wantValid: true, wantValue: "a",
			query: "{\"in\": [\n  {\"var\": \"n\"},\n  [1, 2]\n]}",
		},
		{
			name: "jsonlogic with CRLF line ends", wantValid: true, wantValue: "a",
			query: "{\"in\": [\r\n  {\"var\": \"n\"},\r\n  [1, 2]\r\n]}",
		},
		{
			name: "jsonlogic with leading and trailing blank lines", wantValid: true, wantValue: "a",
			query: "\n\n{\"in\": [{\"var\": \"n\"}, [1, 2]]}\n\n",
		},
		{
			name: "jsonlogic string literal containing a newline is rejected", wantValid: false,
			query: newlineInString,
		},
		{
			name:      "jsonlogic string literal containing a newline is rejected at load in a scheduled step",
			wantValid: false, scheduled: true,
			query: newlineInString,
		},
		{
			name: "nikunjy quoted break after a bracket matches the key joined with one space", wantValid: true,
			key: "ab[ c", wantValue: "a", query: quotedBracketBreak,
		},
		{
			name: "nikunjy quoted break after a bracket does not match the unjoined key", wantValid: true,
			key: "ab[c", wantValue: "b", query: quotedBracketBreak,
		},
		{
			name: "nikunjy quoted break after a bracket does not match the double-spaced key", wantValid: true,
			key: "ab[  c", wantValue: "b", query: quotedBracketBreak,
		},
		{
			name: "nikunjy quoted break after a letter matches the key joined with one space", wantValid: true,
			key: "ab c", wantValue: "a", query: quotedLetterBreak,
		},
		{
			name: "nikunjy quoted break after a letter does not match the unjoined key", wantValid: true,
			key: fixtures.UserKeyAbc, wantValue: "b", query: quotedLetterBreak,
		},
		{
			name: "nikunjy quoted break after a letter does not match the double-spaced key", wantValid: true,
			key: "ab  c", wantValue: "b", query: quotedLetterBreak,
		},
		{
			name: "jsonlogic multi-line in a scheduled step", wantValid: true, wantValue: "a", scheduled: true,
			query: "{\"in\": [\n  {\"var\": \"n\"},\n  [1, 2]\n]}",
		},
		{
			name:     "jsonlogic operands on consecutive lines are not evaluated as one number (scheduled step)",
			evalOnly: true, scheduled: true, n: 12, wantValid: true, wantValue: "b",
			query: consecutiveOperands,
		},
		{
			name:     "jsonlogic operands on consecutive lines are not evaluated as one number",
			evalOnly: true, n: 12, wantValid: true, wantValue: "b",
			query: consecutiveOperands,
		},
		{
			name:     "jsonlogic string literal containing a newline is not evaluated as joined text",
			evalOnly: true, n: 1, wantValid: true, wantValue: "b",
			query: newlineInString,
		},
		{
			name: "nikunjy operator and operand on consecutive lines", wantValid: true, wantValue: "a",
			query: "n eq\n1",
		},
		{
			name: "nikunjy keywords on consecutive lines", wantValid: true, wantValue: "a",
			query: "n eq 1\nand key eq \"abc\"",
		},
		{
			name: "nikunjy list across lines", wantValid: true, wantValue: "a",
			query: "n in [\n1,\n2\n]",
		},
		{
			name: "nikunjy key list of the multi-line query testdata", wantValid: true, wantValue: "a",
			query: "key in [\n\"abc\",\n\"random-key-1\",\n\"random-key-2\"\n]",
		},
		{
			name: "nikunjy with CRLF line ends", wantValid: true, wantValue: "a",
			query: "n eq 1\r\n  and key eq \"abc\"\r\n",
		},
		{
			name: "nikunjy with leading and trailing blank lines", wantValid: true, wantValue: "a",
			query: "\n\nn eq 1\n\n",
		},
		{
			name: "nikunjy upstream-style trailing-space continuation", wantValid: true, wantValue: "a",
			query: "n eq 1 and \n    key eq \"abc\"",
		},
		{
			name: "nikunjy string literal containing a newline does not equal the joined text", wantValid: true,
			wantValue: "b", query: "key eq \"a\nbc\"",
		},
		{
			name: "nikunjy multi-line in a scheduled step", wantValid: true, wantValue: "a", scheduled: true,
			query: "n eq 1\nand key eq \"abc\"",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := flagdef.NewService()
			flg := multilineFlag(tt.query, tt.scheduled)

			var err error
			if !tt.evalOnly {
				err = flg.IsValid(svc)
			}

			if !tt.wantValid {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)

			number := tt.n
			if number == 0 {
				number = 1
			}

			key := tt.key
			if key == "" {
				key = fixtures.UserKeyAbc
			}

			ctx := atFixedNow(ffcontext.NewEvaluationContextBuilder(key).AddCustom("n", number).Build())
			got, _ := flg.Value("flag", ctx, flagdef.Context{Service: svc, DefaultSdkValue: "sdk"})
			assert.Equal(t, tt.wantValue, got)
		})
	}
}

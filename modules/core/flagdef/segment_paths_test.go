package flagdef_test

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
	"github.com/blackbirdworks/semaphore/modules/core/internalerror"
	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

// longQuery pads a query to more than 1024 bytes: the engine then finds its evaluator by the hash that the validation
// saved in the rule, which is another path than the one of the short queries.
const (
	formatNikunjy = "nikunjy"
	formatJSON    = "json"
)

func longQuery(segmentTerm string) string {
	var padding strings.Builder

	for i := range 80 {
		fmt.Fprintf(&padding, `plan eq "padding-%03d" or `, i)
	}

	return padding.String() + segmentTerm
}

func TestSegmentTermsOfALongQuery(t *testing.T) {
	t.Parallel()

	provider := segment.NewMemoryProvider(map[string][]string{segBeta: {keyAlice}})
	query := longQuery(nikunjyInclude)
	require.Greater(t, len(query), 1024)

	svc := flagdef.NewService()
	rule := queryRule("long", query)
	flag := onOffFlag(rule)
	require.NoError(t, flag.IsValid(svc))
	assert.NotZero(t, (*flag.Rules)[0].QueryHash, "the validation saved the hash of the long query")

	tests := []struct {
		name        string
		key         string
		wantVariant string
	}{
		{name: "member", key: keyAlice, wantVariant: variantOn},
		{name: "not member", key: keyBob, wantVariant: variantOff},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, details := flag.Value(
				"f",
				ffcontext.NewEvaluationContext(tt.key),
				flagdef.Context{Service: svc, Segments: provider},
			)
			assert.Equal(t, tt.wantVariant, details.Variant)
			assert.Equal(t, segment.StatusHealthy, details.SegmentStatus)
		})
	}
}

func TestSegmentRuleEvaluatedWithoutValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		onError     *string
		name        string
		query       string
		wantMatched bool
	}{
		{name: "nikunjy, no provider: unknown, skipped", query: nikunjyInclude},
		{
			name:        "nikunjy, no provider: onError match",
			query:       nikunjyInclude,
			onError:     new("match"),
			wantMatched: true,
		},
		{name: "json, no provider: unknown, skipped", query: jsonInclude},
		{
			name:        "json, no provider: onError match",
			query:       jsonInclude,
			onError:     new("match"),
			wantMatched: true,
		},
		{name: "invalid segment term: never matches", query: `targetingKey in segment("Bad")`, onError: new("match")},
		{
			name:    "invalid json segment term: never matches",
			query:   `{"some": [[1], {"inSegment": [{"var": "k"}, "a"]}]}`,
			onError: new("match"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rule := queryRule("r", tt.query)
			rule.OnError = tt.onError

			variant, err := rule.Evaluate(
				flagdef.NewService(),
				keyAlice,
				ffcontext.NewEvaluationContext(keyAlice),
				"f",
				false,
			)
			if tt.wantMatched {
				require.NoError(t, err)
				assert.Equal(t, variantOn, variant)

				return
			}

			var notApply *internalerror.RuleNotApplyError

			require.ErrorAs(t, err, &notApply)
		})
	}
}

// lookupsOf is the lookups of the rule of TestSegmentRuleWithManyTerms for a key: one for each segment s00, s01, ...
func lookupsOf(terms int, key string) []string {
	lookups := make([]string, 0, terms)
	for i := range terms {
		lookups = append(lookups, fmt.Sprintf("s%02d:%s", i, key))
	}

	return lookups
}

func TestSegmentRuleWithManyTerms(t *testing.T) {
	t.Parallel()

	const terms = 12

	members := map[string][]string{}
	nikunjy := make([]string, 0, terms)
	json := make([]string, 0, terms)

	for i := range terms {
		name := fmt.Sprintf("s%02d", i)
		members[name] = []string{keyAlice}

		nikunjy = append(nikunjy, fmt.Sprintf("targetingKey in segment(%q)", name))
		json = append(json, fmt.Sprintf(`{"inSegment": [{"var": "targetingKey"}, %q]}`, name))
	}

	members["s11"] = []string{keyBob} // the last term decides

	queries := map[string]string{
		formatNikunjy: strings.Join(nikunjy, " and "),
		formatJSON:    `{"and": [` + strings.Join(json, ", ") + `]}`,
	}

	for name, query := range queries {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			svc := flagdef.NewService()
			counting := &recordingProvider{inner: segment.NewMemoryProvider(members)}
			flag := onOffFlag(queryRule("many", query))
			require.NoError(t, flag.IsValid(svc))

			flagCtx := flagdef.Context{Service: svc, Segments: counting}

			_, notAll := flag.Value("f", ffcontext.NewEvaluationContext(keyAlice), flagCtx)
			assert.Equal(t, variantOff, notAll.Variant, "alice is not in s11")
			assert.Equal(t, [][]string{lookupsOf(terms, keyAlice)}, counting.calls, "one call with every term")

			_, all := flag.Value("f", ffcontext.NewEvaluationContext(keyBob), flagCtx)
			assert.Equal(t, variantOff, all.Variant, "bob is only in s11")
			assert.Equal(t, [][]string{lookupsOf(terms, keyAlice), lookupsOf(terms, keyBob)}, counting.calls)
		})
	}
}

func TestSegmentRuleWithTooManyTerms(t *testing.T) {
	t.Parallel()

	nikunjy := make([]string, 0, 33)
	json := make([]string, 0, 33)

	for i := range 33 {
		nikunjy = append(nikunjy, fmt.Sprintf(`targetingKey in segment("s%02d")`, i))
		json = append(json, fmt.Sprintf(`{"inSegment": [{"var": "targetingKey"}, "s%02d"]}`, i))
	}

	for name, query := range map[string]string{
		formatNikunjy: strings.Join(nikunjy, " or "),
		formatJSON:    `{"or": [` + strings.Join(json, ", ") + `]}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := onOffFlag(queryRule("many", query)).IsValid(flagdef.NewService())
			require.ErrorIs(t, err, flagdef.ErrInvalidSegment)
			assert.Contains(t, err.Error(), "32")
		})
	}
}

// panicProvider is a provider that crashes.
type panicProvider struct{}

func (panicProvider) Contains(context.Context, []segment.Lookup) error { panic("provider crashed") }

func TestSegmentEvaluationErrorsDoNotMatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		attrs    map[string]any
		provider segment.Provider
		name     string
		query    string
		wantLog  string
	}{
		{
			name:     "nikunjy: an invalid operation in another term",
			query:    `plan gt true and targetingKey in segment("beta")`,
			provider: segment.NewMemoryProvider(map[string][]string{segBeta: {keyAlice}}),
			attrs:    map[string]any{"plan": planPro},
			wantLog:  "error while evaluating the nikunjy query",
		},
		{
			name: "jsonlogic: a leaf that fails when it is applied",
			query: `{"and": [{"inSegment": [{"var": "targetingKey"}, "beta"]}, ` +
				`{"map": [{"var": "targetingKey"}, {"var": ""}]}]}`,
			provider: segment.NewMemoryProvider(map[string][]string{segBeta: {keyAlice}}),
			wantLog:  "error while evaluating the jsonlogic query",
		},
		{
			name:     "jsonlogic: a context the library cannot read",
			query:    `{"and": [{"inSegment": [{"var": "targetingKey"}, "beta"]}, {"==": [{"var": "ch"}, 1]}]}`,
			provider: segment.NewMemoryProvider(map[string][]string{segBeta: {keyAlice}}),
			attrs:    map[string]any{"ch": make(chan int)},
			wantLog:  "error while evaluating the jsonlogic query",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf syncBuffer

			svc := flagdef.NewService(flagdef.WithLogger(slog.New(slog.NewTextHandler(&buf, nil))))
			rule := queryRule("r", tt.query)
			rule.OnError = new("match") // an error is not an unknown result: it never matches

			flag := onOffFlag(rule)
			require.NoError(t, flag.IsValid(svc))

			builder := ffcontext.NewEvaluationContextBuilder(keyAlice)
			for key, value := range tt.attrs {
				builder.AddCustom(key, value)
			}

			_, details := flag.Value("f", builder.Build(), flagdef.Context{Service: svc, Segments: tt.provider})

			assert.Equal(t, variantOff, details.Variant)
			assert.Contains(t, buf.String(), tt.wantLog)
		})
	}
}

func TestSegmentJSONLogicOperators(t *testing.T) {
	t.Parallel()

	svc := flagdef.NewService()
	provider := segment.NewMemoryProvider(map[string][]string{segBeta: {keyAlice}})

	tests := []struct {
		name        string
		query       string
		key         string
		wantVariant string
	}{
		{
			name:        "!! of a member",
			query:       `{"!!": {"inSegment": [{"var": "targetingKey"}, "beta"]}}`,
			key:         keyAlice,
			wantVariant: variantOn,
		},
		{
			name:        "!! of a non member",
			query:       `{"!!": [{"inSegment": [{"var": "targetingKey"}, "beta"]}]}`,
			key:         keyBob,
			wantVariant: variantOff,
		},
		{
			name:        "! of a non member",
			query:       `{"!": {"inSegment": [{"var": "targetingKey"}, "beta"]}}`,
			key:         keyBob,
			wantVariant: variantOn,
		},
		{
			name:        "nested attribute",
			query:       `{"inSegment": [{"var": "user.id"}, "beta"]}`,
			key:         keyBob,
			wantVariant: variantOff,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			flag := onOffFlag(queryRule("r", tt.query))
			require.NoError(t, flag.IsValid(svc))

			_, details := flag.Value(
				"f",
				ffcontext.NewEvaluationContext(tt.key),
				flagdef.Context{Service: svc, Segments: provider},
			)
			assert.Equal(t, tt.wantVariant, details.Variant)
		})
	}
}

func TestSegmentNestedAttribute(t *testing.T) {
	t.Parallel()

	svc := flagdef.NewService()

	for name, query := range map[string]string{
		formatNikunjy: `user.id in segment("beta")`,
		formatJSON:    `{"inSegment": [{"var": "user.id"}, "beta"]}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			provider := &recordingProvider{inner: segment.NewMemoryProvider(map[string][]string{segBeta: {keyAlice}})}
			flag := onOffFlag(queryRule("r", query))
			require.NoError(t, flag.IsValid(svc))

			evalCtx := ffcontext.NewEvaluationContextBuilder("k").
				AddCustom("user", map[string]any{"id": keyAlice}).
				Build()
			_, member := flag.Value("f", evalCtx, flagdef.Context{Service: svc, Segments: provider})
			assert.Equal(t, variantOn, member.Variant)

			other := ffcontext.NewEvaluationContextBuilder("k").AddCustom("user", map[string]any{"id": keyBob}).Build()
			_, notMember := flag.Value("f", other, flagdef.Context{Service: svc, Segments: provider})
			assert.Equal(t, variantOff, notMember.Variant)

			_, noUser := flag.Value(
				"f",
				ffcontext.NewEvaluationContext("k"),
				flagdef.Context{Service: svc, Segments: provider},
			)
			assert.Equal(t, variantOff, noUser.Variant, "no user attribute: not a member")
			assert.Equal(t, [][]string{{"beta:alice"}, {"beta:bob"}}, provider.calls, "and no lookup for it")
		})
	}
}

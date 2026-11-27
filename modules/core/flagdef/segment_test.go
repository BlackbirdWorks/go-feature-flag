package flagdef_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

const (
	variantOn  = "on"
	variantOff = "off"

	keyAlice           = "alice"
	keyBob             = "bob"
	planPro            = "pro"
	planFree           = "free"
	segBeta            = "beta"
	segChurned         = "churned"
	keyCarol           = "carol"
	segOther           = "other"
	whereDefaultRule   = "defaultRule"
	segIDs             = "ids"
	lookupIDsAlice     = "ids:alice"
	errMsgBadName      = "invalid segment name"
	errMsgBadJSONLogic = "invalid jsonlogic query"
	errMsgNotAllowed   = "not allowed"
	errMsgBadOnError   = "invalid onError"
	wherePathAttr      = "$.inSegment[0]"
	queryEqAlice       = `targetingKey eq "alice"`
)

var errStoreDown = errors.New("store down")

// failingProvider is a provider whose store is down.
type failingProvider struct{}

func (failingProvider) Contains(context.Context, []segment.Lookup) error { return errStoreDown }

// silentProvider answers nothing and reports no error: every lookup stays unknown.
type silentProvider struct{}

func (silentProvider) Contains(context.Context, []segment.Lookup) error { return nil }

func onOffFlag(rules ...flagdef.Rule) *flagdef.InternalFlag {
	return &flagdef.InternalFlag{
		Variations: &map[string]*any{variantOn: new(any(true)), variantOff: new(any(false))},
		Rules:      &rules,
		DefaultRule: &flagdef.Rule{
			VariationResult: new(variantOff),
		},
	}
}

func queryRule(name, query string) flagdef.Rule {
	return flagdef.Rule{Name: new(name), Query: new(query), VariationResult: new(variantOn)}
}

func mustCatalog(t *testing.T, defs ...segment.Definition) *segment.Catalog {
	t.Helper()

	catalog, err := segment.NewCatalog(defs...)
	require.NoError(t, err)

	return catalog
}

const (
	nikunjyInclude = `targetingKey in segment("beta")`
	jsonInclude    = `{"inSegment": [{"var": "targetingKey"}, "beta"]}`
	nikunjyMixed   = `targetingKey in segment("beta") and not (targetingKey in segment("churned"))`
	jsonMixed      = `{"and": [{"inSegment": [{"var": "targetingKey"}, "beta"]}, ` +
		`{"!": {"inSegment": [{"var": "targetingKey"}, "churned"]}}]}`
	nikunjyOr = `targetingKey in segment("beta") or targetingKey in segment("churned")`
	jsonOr    = `{"or": [{"inSegment": [{"var": "targetingKey"}, "beta"]}, ` +
		`{"inSegment": [{"var": "targetingKey"}, "churned"]}]}`
	nikunjyPlan    = `plan eq "pro" or targetingKey in segment("beta")`
	jsonPlan       = `{"or": [{"==": [{"var": "plan"}, "pro"]}, {"inSegment": [{"var": "targetingKey"}, "beta"]}]}`
	nikunjyPlanAnd = `plan eq "pro" and targetingKey in segment("beta")`
	jsonPlanAnd    = `{"and": [{"==": [{"var": "plan"}, "pro"]}, {"inSegment": [{"var": "targetingKey"}, "beta"]}]}`
)

// evalCase is a flag with one targeting rule, evaluated for one key.
type evalCase struct {
	provider      segment.Provider
	reqCtx        context.Context //nolint:containedctx // the table carries the request context of each case
	catalog       *segment.Catalog
	onError       *string
	name          string
	query         string
	key           string
	plan          string
	wantVariant   string
	wantStatus    segment.Status
	wantCacheable bool
}

func healthyEvalCases(_ context.Context, t *testing.T) []evalCase {
	t.Helper()

	return []evalCase{
		// member / not member, in both formats
		{
			name:        "nikunjy member",
			query:       nikunjyInclude,
			key:         keyAlice,
			wantVariant: variantOn,
			wantStatus:  segment.StatusHealthy,
		},
		{
			name:        "json member",
			query:       jsonInclude,
			key:         keyAlice,
			wantVariant: variantOn,
			wantStatus:  segment.StatusHealthy,
		},
		{
			name:        "nikunjy not member",
			query:       nikunjyInclude,
			key:         keyBob,
			wantVariant: variantOff,
			wantStatus:  segment.StatusHealthy,
		},
		{
			name:        "json not member",
			query:       jsonInclude,
			key:         keyBob,
			wantVariant: variantOff,
			wantStatus:  segment.StatusHealthy,
		},
		// include and exclude
		{
			name:        "nikunjy include exclude: ok",
			query:       nikunjyMixed,
			key:         keyAlice,
			wantVariant: variantOn,
			wantStatus:  segment.StatusHealthy,
		},
		{
			name:        "json include exclude: ok",
			query:       jsonMixed,
			key:         keyAlice,
			wantVariant: variantOn,
			wantStatus:  segment.StatusHealthy,
		},
		{
			name:        "nikunjy include exclude: excluded",
			query:       nikunjyMixed,
			key:         keyCarol,
			wantVariant: variantOff,
			wantStatus:  segment.StatusHealthy,
		},
		{
			name:        "json include exclude: excluded",
			query:       jsonMixed,
			key:         keyCarol,
			wantVariant: variantOff,
			wantStatus:  segment.StatusHealthy,
		},
		// or
		{
			name:        "nikunjy or: second",
			query:       nikunjyOr,
			key:         keyBob,
			wantVariant: variantOn,
			wantStatus:  segment.StatusHealthy,
		},
		{
			name:        "json or: second",
			query:       jsonOr,
			key:         keyBob,
			wantVariant: variantOn,
			wantStatus:  segment.StatusHealthy,
		},
		{
			name:        "nikunjy or: none",
			query:       nikunjyOr,
			key:         "dave",
			wantVariant: variantOff,
			wantStatus:  segment.StatusHealthy,
		},
		{name: "json or: none", query: jsonOr, key: "dave", wantVariant: variantOff, wantStatus: segment.StatusHealthy},
		// missing targeting key: not a member, nothing is looked up
		{
			name:          "nikunjy missing key",
			query:         nikunjyInclude,
			key:           "",
			wantVariant:   variantOff,
			wantStatus:    segment.StatusNone,
			wantCacheable: true,
		},
		{
			name:          "json missing key",
			query:         jsonInclude,
			key:           "",
			wantVariant:   variantOff,
			wantStatus:    segment.StatusNone,
			wantCacheable: true,
		},
	}
}

func failingEvalCases(cancelled context.Context, t *testing.T) []evalCase {
	t.Helper()

	return []evalCase{
		// the store is down: the rule is skipped...
		{
			name:        "nikunjy store error",
			query:       nikunjyInclude,
			key:         keyAlice,
			provider:    failingProvider{},
			wantVariant: variantOff,
			wantStatus:  segment.StatusStoreError,
		},
		{
			name:        "json store error",
			query:       jsonInclude,
			key:         keyAlice,
			provider:    failingProvider{},
			wantVariant: variantOff,
			wantStatus:  segment.StatusStoreError,
		},
		// ... unless the rule says onError match
		{
			name:        "nikunjy store error, onError match",
			query:       nikunjyInclude,
			key:         keyAlice,
			provider:    failingProvider{},
			onError:     new("match"),
			wantVariant: variantOn,
			wantStatus:  segment.StatusStoreError,
		},
		{
			name:        "json store error, onError match",
			query:       jsonInclude,
			key:         keyAlice,
			provider:    failingProvider{},
			onError:     new("match"),
			wantVariant: variantOn,
			wantStatus:  segment.StatusStoreError,
		},
		{
			name:        "store error, onError skip",
			query:       nikunjyInclude,
			key:         keyAlice,
			provider:    failingProvider{},
			onError:     new("skip"),
			wantVariant: variantOff,
			wantStatus:  segment.StatusStoreError,
		},
		// the provider answers nothing
		{
			name:        "unanswered lookup",
			query:       nikunjyInclude,
			key:         keyAlice,
			provider:    silentProvider{},
			wantVariant: variantOff,
			wantStatus:  segment.StatusStoreError,
		},
		// unknown segment of the provider
		{
			name:        "segment unknown to the provider",
			query:       `targetingKey in segment("other")`,
			key:         keyAlice,
			wantVariant: variantOff,
			wantStatus:  segment.StatusStoreError,
		},
		// cancelled request
		{
			name:        "nikunjy cancelled",
			query:       nikunjyInclude,
			key:         keyAlice,
			reqCtx:      cancelled,
			wantVariant: variantOff,
			wantStatus:  segment.StatusStoreError,
		},
		{
			name:        "json cancelled, onError match",
			query:       jsonInclude,
			key:         keyAlice,
			reqCtx:      cancelled,
			onError:     new("match"),
			wantVariant: variantOn,
			wantStatus:  segment.StatusStoreError,
		},
		// no provider
		{
			name:        "no provider",
			query:       nikunjyInclude,
			key:         keyAlice,
			provider:    nilProvider{},
			wantVariant: variantOff,
			wantStatus:  segment.StatusNotConfigured,
		},
		{
			name:        "no provider, onError match",
			query:       jsonInclude,
			key:         keyAlice,
			provider:    nilProvider{},
			onError:     new("match"),
			wantVariant: variantOn,
			wantStatus:  segment.StatusNotConfigured,
		},
	}
}

func policyEvalCases(_ context.Context, t *testing.T) []evalCase {
	t.Helper()

	return []evalCase{
		// outcome independent of the unknown terms
		{
			name:          "or: true attribute decides, store down",
			query:         nikunjyPlan,
			key:           keyAlice,
			plan:          planPro,
			provider:      failingProvider{},
			wantVariant:   variantOn,
			wantStatus:    segment.StatusNone,
			wantCacheable: true,
		},
		{
			name:          "json or: true attribute decides, store down",
			query:         jsonPlan,
			key:           keyAlice,
			plan:          planPro,
			provider:      failingProvider{},
			wantVariant:   variantOn,
			wantStatus:    segment.StatusNone,
			wantCacheable: true,
		},
		{
			name:          "and: false attribute decides, store down, onError match",
			query:         nikunjyPlanAnd,
			key:           keyAlice,
			plan:          planFree,
			provider:      failingProvider{},
			onError:       new("match"),
			wantVariant:   variantOff,
			wantStatus:    segment.StatusNone,
			wantCacheable: true,
		},
		{
			name:          "json and: false attribute decides, store down, onError match",
			query:         jsonPlanAnd,
			key:           keyAlice,
			plan:          planFree,
			provider:      failingProvider{},
			onError:       new("match"),
			wantVariant:   variantOff,
			wantStatus:    segment.StatusNone,
			wantCacheable: true,
		},
		{
			name:        "and: true attribute depends on the store, onError match",
			query:       nikunjyPlanAnd,
			key:         keyAlice,
			plan:        planPro,
			provider:    failingProvider{},
			onError:     new("match"),
			wantVariant: variantOn,
			wantStatus:  segment.StatusStoreError,
		},
		// failure policy of the definition
		{
			name:     "policy notMember",
			query:    `not (targetingKey in segment("beta"))`,
			key:      keyAlice,
			provider: failingProvider{},
			catalog: mustCatalog(
				t,
				segment.Definition{Name: segBeta, Type: segment.TypeValkey, OnError: segment.OnErrorNotMember},
			),
			wantVariant: variantOn,
			wantStatus:  segment.StatusStoreError,
		},
		{
			name:     "policy member",
			query:    nikunjyInclude,
			key:      keyAlice,
			provider: failingProvider{},
			catalog: mustCatalog(
				t,
				segment.Definition{Name: segBeta, Type: segment.TypeValkey, OnError: segment.OnErrorMember},
			),
			wantVariant: variantOn,
			wantStatus:  segment.StatusStoreError,
		},
		{
			name:     "policy unknown",
			query:    nikunjyInclude,
			key:      keyAlice,
			provider: failingProvider{},
			catalog: mustCatalog(
				t,
				segment.Definition{Name: segBeta, Type: segment.TypeValkey, OnError: segment.OnErrorUnknown},
			),
			wantVariant: variantOff,
			wantStatus:  segment.StatusStoreError,
		},
		{
			name: "defined segment, healthy", query: nikunjyInclude, key: keyAlice,
			catalog:     mustCatalog(t, segment.Definition{Name: segBeta, Type: segment.TypeFile}),
			wantVariant: variantOn, wantStatus: segment.StatusHealthy,
		},
		{
			name: "undefined segment matches nobody", query: nikunjyInclude, key: keyAlice,
			catalog:     mustCatalog(t, segment.Definition{Name: segOther, Type: segment.TypeFile}),
			wantVariant: variantOff, wantStatus: segment.StatusNone,
			wantCacheable: true,
		},
		{
			name:          "undefined segment under not matches everybody",
			query:         `not (targetingKey in segment("beta"))`,
			key:           keyAlice,
			catalog:       mustCatalog(t, segment.Definition{Name: segOther, Type: segment.TypeFile}),
			wantVariant:   variantOn,
			wantStatus:    segment.StatusNone,
			wantCacheable: true,
		},
	}
}

func TestSegmentRuleEvaluation(t *testing.T) {
	t.Parallel()

	members := segment.NewMemoryProvider(map[string][]string{
		segBeta:    {keyAlice, keyCarol},
		segChurned: {keyBob, keyCarol},
	})
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	tests := healthyEvalCases(cancelled, t)
	tests = append(tests, failingEvalCases(cancelled, t)...)
	tests = append(tests, policyEvalCases(cancelled, t)...)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var opts []flagdef.ServiceOption
			if tt.catalog != nil {
				opts = append(opts, flagdef.WithSegments(tt.catalog))
			}

			svc := flagdef.NewService(opts...)
			t.Cleanup(svc.Close)

			rule := queryRule("targeted", tt.query)
			rule.OnError = tt.onError
			flag := onOffFlag(rule)
			require.NoError(t, flag.IsValid(svc))

			provider := tt.provider
			switch provider.(type) {
			case nil:
				provider = members
			case nilProvider:
				provider = nil
			}

			evalCtx := ffcontext.NewEvaluationContextBuilder(tt.key).AddCustom("plan", tt.plan).Build()
			_, details := flag.Value(
				"flag",
				evalCtx,
				flagdef.Context{Service: svc, Segments: provider, RequestContext: tt.reqCtx},
			)

			assert.Equal(t, tt.wantVariant, details.Variant)
			assert.Equal(t, tt.wantStatus, details.SegmentStatus)
			assert.Equal(t, tt.wantCacheable, details.Cacheable, "a value that read a segment is not cacheable")
		})
	}
}

// nilProvider marks the cases that run without a provider.
type nilProvider struct{}

func (nilProvider) Contains(context.Context, []segment.Lookup) error { return nil }

func TestSegmentEvaluationReasonStaysTheSame(t *testing.T) {
	t.Parallel()

	svc := flagdef.NewService()
	flag := onOffFlag(queryRule("r", `targetingKey in segment("beta")`))
	require.NoError(t, flag.IsValid(svc))

	provider := segment.NewMemoryProvider(map[string][]string{segBeta: {keyAlice}})

	tests := []struct {
		name       string
		key        string
		wantReason flagdef.ResolutionReason
		wantRule   string
	}{
		{name: "member", key: keyAlice, wantReason: flagdef.ReasonTargetingMatch, wantRule: "r"},
		{name: "not member", key: keyBob, wantReason: flagdef.ReasonDefault},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, details := flag.Value(
				"f",
				ffcontext.NewEvaluationContext(tt.key),
				flagdef.Context{Service: svc, Segments: provider},
			)
			assert.Equal(t, tt.wantReason, details.Reason)

			if tt.wantRule != "" {
				require.NotNil(t, details.RuleName)
				assert.Equal(t, tt.wantRule, *details.RuleName)
			} else {
				assert.Nil(t, details.RuleName)
			}
		})
	}
}

func TestSegmentLookupsOfARuleAreOneCall(t *testing.T) {
	t.Parallel()

	svc := flagdef.NewService()
	provider := &recordingProvider{
		inner: segment.NewMemoryProvider(map[string][]string{"a": {"k"}, "b": {"k"}, "c": {"x"}}),
	}
	flag := onOffFlag(
		queryRule(
			"r",
			`targetingKey in segment("a") and targetingKey in segment("b") and targetingKey in segment("c")`,
		),
	)
	require.NoError(t, flag.IsValid(svc))

	_, details := flag.Value(
		"f",
		ffcontext.NewEvaluationContext("k"),
		flagdef.Context{Service: svc, Segments: provider},
	)

	assert.Equal(t, variantOff, details.Variant)
	assert.Equal(t, [][]string{{"a:k", "b:k", "c:k"}}, provider.calls, "one call with the three lookups")
}

func TestSegmentNotLookedUpForARuleThatIsNotReached(t *testing.T) {
	t.Parallel()

	svc := flagdef.NewService()
	provider := &recordingProvider{inner: segment.NewMemoryProvider(nil)}
	first := flagdef.Rule{Name: new("first"), Query: new(`plan eq "pro"`), VariationResult: new(variantOn)}
	flag := onOffFlag(first, queryRule("second", `targetingKey in segment("a")`))
	require.NoError(t, flag.IsValid(svc))

	evalCtx := ffcontext.NewEvaluationContextBuilder("k").AddCustom("plan", planPro).Build()
	_, details := flag.Value("f", evalCtx, flagdef.Context{Service: svc, Segments: provider})

	assert.Equal(t, variantOn, details.Variant)
	assert.Empty(t, provider.calls)
	assert.Equal(t, segment.StatusNone, details.SegmentStatus)
	assert.True(t, details.Cacheable, "the first rule decided it without a segment")
}

func TestSegmentAttributeTypes(t *testing.T) {
	t.Parallel()

	svc := flagdef.NewService()
	provider := segment.NewMemoryProvider(map[string][]string{segIDs: {"42", "x"}})
	flag := onOffFlag(queryRule("r", `userId in segment("ids")`))
	require.NoError(t, flag.IsValid(svc))

	tests := []struct {
		attr        any
		name        string
		wantVariant string
	}{
		{name: "string value", attr: "x", wantVariant: variantOn},
		{name: "int", attr: 42, wantVariant: variantOn},
		{name: "int64", attr: int64(42), wantVariant: variantOn},
		{name: "another int", attr: 7, wantVariant: variantOff},
		{name: "blank string", attr: "", wantVariant: variantOff},
		{name: "bool", attr: true, wantVariant: variantOff},
		{name: "float", attr: 42.5, wantVariant: variantOff},
		{name: "list", attr: []any{"x"}, wantVariant: variantOff},
		{name: "missing", attr: nil, wantVariant: variantOff},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			builder := ffcontext.NewEvaluationContextBuilder("k")
			if tt.attr != nil {
				builder.AddCustom("userId", tt.attr)
			}

			_, details := flag.Value("f", builder.Build(), flagdef.Context{Service: svc, Segments: provider})
			assert.Equal(t, tt.wantVariant, details.Variant)
		})
	}
}

func TestSegmentAttributeThatIsNamedSegmentIsNotTheFunction(t *testing.T) {
	t.Parallel()

	svc := flagdef.NewService()
	provider := &recordingProvider{inner: segment.NewMemoryProvider(map[string][]string{segBeta: {keyAlice}})}
	flag := onOffFlag(queryRule("r", `segment eq "beta"`))
	require.NoError(t, flag.IsValid(svc))

	evalCtx := ffcontext.NewEvaluationContextBuilder(keyAlice).AddCustom("segment", "beta").Build()
	_, details := flag.Value("f", evalCtx, flagdef.Context{Service: svc, Segments: provider})

	assert.Equal(t, variantOn, details.Variant)
	assert.Empty(t, provider.calls, "no lookup: it is an attribute")
	assert.Equal(t, segment.StatusNone, details.SegmentStatus)
	assert.True(t, details.Cacheable)
}

func TestSegmentRuleOnErrorValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		onError string
		name    string
		wantErr string
	}{
		{name: "skip", onError: ruleOnErrorSkip},
		{name: "match", onError: ruleOnErrorMatch},
		{name: "unknown value", onError: "ignore", wantErr: errMsgBadOnError},
		{name: "no value", onError: "", wantErr: errMsgBadOnError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rule := queryRule("r", `targetingKey in segment("a")`)
			rule.OnError = &tt.onError

			err := onOffFlag(rule).IsValid(flagdef.NewService())
			if tt.wantErr == "" {
				require.NoError(t, err)

				return
			}

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestSegmentRuleValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		rule       flagdef.Rule
		name       string
		wantInErr  []string
		defaultSeg bool
	}{
		{name: "valid nikunjy", rule: queryRule("r", `targetingKey in segment("a")`)},
		{name: "valid json", rule: queryRule("r", `{"inSegment": [{"var": "targetingKey"}, "a"]}`)},
		{
			name: "valid json, the operator written with a unicode escape",
			rule: queryRule("r", `{"in\u0053egment": [{"var": "targetingKey"}, "a"]}`),
		},
		{
			name: "valid json nested",
			rule: queryRule(
				"r",
				`{"and": [{"or": [{"!": [{"inSegment": [{"var": "k"}, "a"]}]}, {"==": [1, 1]}]}, `+
					`{"!!": {"inSegment": [{"var": "a.b"}, "b"]}}]}`,
			),
		},
		{
			name:      "nikunjy bad name names the rule and the offset",
			rule:      queryRule("vip", `age gt 1 and key in segment("Bad")`),
			wantInErr: []string{`targeting rule "vip" (index 0)`, errMsgBadName, "offset 29"},
		},
		{
			name: "nikunjy name not a literal", rule: queryRule("vip", `key in segment(beta)`),
			wantInErr: []string{`"vip"`, "string literal"},
		},
		{
			name: "nikunjy outside in", rule: queryRule("", `key eq segment("a")`),
			wantInErr: []string{"targeting rule (index 0)", `right side of "in"`},
		},
		{
			name: "json name not a literal", rule: queryRule("vip", `{"inSegment": [{"var": "k"}, {"var": "name"}]}`),
			wantInErr: []string{`"vip"`, "$.inSegment[1]", "string literal"},
		},
		{
			name: "json bad name", rule: queryRule("vip", `{"inSegment": [{"var": "k"}, "A B"]}`),
			wantInErr: []string{"$.inSegment[1]", errMsgBadName},
		},
		{
			name: "json one operand", rule: queryRule("vip", `{"inSegment": [{"var": "k"}]}`),
			wantInErr: []string{"$.inSegment", "two operands"},
		},
		{
			name: "json attribute not a var", rule: queryRule("vip", `{"inSegment": ["k", "a"]}`),
			wantInErr: []string{wherePathAttr, `{"var"`},
		},
		{
			name:      "json in an iterator",
			rule:      queryRule("vip", `{"some": [{"var": "ids"}, {"inSegment": [{"var": ""}, "a"]}]}`),
			wantInErr: []string{`"some"`, "$"},
		},
		{
			name:      "json in an iterator under and",
			rule:      queryRule("vip", `{"and": [{"==": [1, 1]}, {"map": [[1], {"inSegment": [{"var": ""}, "a"]}]}]}`),
			wantInErr: []string{`"map"`, "$.and[1]"},
		},
		{
			name: "json under if", rule: queryRule("vip", `{"if": [{"inSegment": [{"var": "k"}, "a"]}, true, false]}`),
			wantInErr: []string{`"if"`},
		},
		{
			name:      "json empty and",
			rule:      queryRule("vip", `{"and": [{"inSegment": [{"var": "k"}, "a"]}, {"and": []}]}`),
			wantInErr: []string{"$.and[1].and", "non-empty"},
		},
		{
			name: "json scalar operand is a valid leaf",
			rule: queryRule("vip", `{"and": [{"inSegment": [{"var": "k"}, "a"]}, true]}`),
		},
		{
			name:      "json leaf that jsonlogic rejects",
			rule:      queryRule("vip", `{"and": [{"inSegment": [{"var": "k"}, "a"]}, {"bogus": [1]}]}`),
			wantInErr: []string{"$.and[1]", errMsgBadJSONLogic},
		},
		{
			name:      "json attribute var is not a string",
			rule:      queryRule("vip", `{"inSegment": [{"var": 5}, "a"]}`),
			wantInErr: []string{wherePathAttr, "with a name"},
		},
		{
			name:      "json attribute var is empty",
			rule:      queryRule("vip", `{"inSegment": [{"var": ""}, "a"]}`),
			wantInErr: []string{wherePathAttr, "with a name"},
		},
		{
			name:      "json bad term under not",
			rule:      queryRule("vip", `{"!": {"inSegment": ["bad", "a"]}}`),
			wantInErr: []string{"$.!.inSegment[0]"},
		},
		{
			name: "json two operands of not", rule: queryRule("vip", `{"!": [{"inSegment": [{"var": "k"}, "a"]}, 1]}`),
			wantInErr: []string{"one operand"},
		},
		{
			name: "default rule with a nikunjy segment", defaultSeg: true,
			rule:      flagdef.Rule{Query: new(`key in segment("a")`), VariationResult: new(variantOff)},
			wantInErr: []string{whereDefaultRule, errMsgNotAllowed},
		},
		{
			name: "default rule with a json segment", defaultSeg: true,
			rule:      flagdef.Rule{Query: new(`{"inSegment": [{"var": "k"}, "a"]}`), VariationResult: new(variantOff)},
			wantInErr: []string{whereDefaultRule, errMsgNotAllowed},
		},
		{
			name: "default rule with a malformed segment", defaultSeg: true,
			rule:      flagdef.Rule{Query: new(`key in segment(`), VariationResult: new(variantOff)},
			wantInErr: []string{whereDefaultRule},
		},
		{
			name: "default rule with an ignored plain query", defaultSeg: true,
			rule: flagdef.Rule{Query: new(`key eq "a"`), VariationResult: new(variantOff)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			flag := onOffFlag(tt.rule)
			if tt.defaultSeg {
				flag = onOffFlag()
				flag.DefaultRule = &tt.rule
			}

			err := flag.IsValid(flagdef.NewService())
			if len(tt.wantInErr) == 0 {
				require.NoError(t, err)

				return
			}

			require.Error(t, err)

			for _, want := range tt.wantInErr {
				assert.Contains(t, err.Error(), want)
			}

			if !strings.Contains(err.Error(), errMsgBadJSONLogic) {
				assert.ErrorIs(t, err, flagdef.ErrInvalidSegment)
			}
		})
	}
}

func TestSegmentRuleIndexIsInTheError(t *testing.T) {
	t.Parallel()

	flag := onOffFlag(queryRule("a", `age gt 1`), queryRule("b", `age gt 2`), queryRule("", `key in segment("X")`))

	err := flag.IsValid(flagdef.NewService())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "targeting rule (index 2)")
}

func TestSegmentWarningForAnUndefinedSegment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		catalog     *segment.Catalog
		name        string
		query       string
		wantWarning bool
	}{
		{name: "no definitions at all: no warning", query: `key in segment("ghost")`},
		{
			name: "defined", query: `key in segment("beta")`,
			catalog: mustCatalog(t, segment.Definition{Name: segBeta, Type: segment.TypeFile}),
		},
		{
			name: "undefined nikunjy", query: `key in segment("ghost")`, wantWarning: true,
			catalog: mustCatalog(t, segment.Definition{Name: segBeta, Type: segment.TypeFile}),
		},
		{
			name: "undefined json", query: `{"inSegment": [{"var": "k"}, "ghost"]}`, wantWarning: true,
			catalog: mustCatalog(t, segment.Definition{Name: segBeta, Type: segment.TypeFile}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf syncBuffer

			logger := slog.New(slog.NewTextHandler(&buf, nil))
			svc := flagdef.NewService(flagdef.WithLogger(logger), flagdef.WithSegments(tt.catalog))

			require.NoError(t, onOffFlag(queryRule("r", tt.query)).IsValid(svc), "an undefined segment is not an error")

			if tt.wantWarning {
				assert.Contains(t, buf.String(), "level=WARN")
				assert.Contains(t, buf.String(), "segment=ghost")
				assert.Contains(t, buf.String(), `index 0`)
			} else {
				assert.NotContains(t, buf.String(), "ghost")
				assert.NotContains(t, buf.String(), "level=WARN")
			}
		})
	}
}

type syncBuffer struct {
	buf bytes.Buffer
	mu  sync.Mutex
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

func TestSegmentNames(t *testing.T) {
	t.Parallel()

	step := flagdef.ScheduledStep{Rules: &[]flagdef.Rule{queryRule("late", `key in segment("later")`)}}
	flag := onOffFlag(
		queryRule("a", `key in segment("zeta") or email in segment("alpha")`),
		queryRule("b", `{"inSegment": [{"var": "k"}, "alpha"]}`),
		queryRule("c", `age gt 1`),
	)
	flag.Scheduled = &[]flagdef.ScheduledStep{step}
	svc := flagdef.NewService()

	assert.Equal(t, []string{"alpha", "later", "zeta"}, flag.SegmentNames(svc))
	assert.True(t, flag.UsesSegments(svc))

	noSegments := onOffFlag(
		queryRule("a", `age gt 1`),
		queryRule("b", `{"==": [{"var": "k"}, 1]}`),
		queryRule("c", `key in [`),
		queryRule("d", `segment in [`),
		flagdef.Rule{VariationResult: new(variantOn)},
	)
	assert.Empty(t, noSegments.SegmentNames(svc))
	assert.Empty(t, onOffFlag(queryRule("c", `age gt 1`)).SegmentNames(svc))
	assert.False(t, onOffFlag(queryRule("c", `age gt 1`)).UsesSegments(svc))
}

func TestSegmentRuleMergeAndCopyKeepOnError(t *testing.T) {
	t.Parallel()

	rule := queryRule("r", `key in segment("a")`)
	rule.MergeRules(flagdef.Rule{OnError: new("match")})
	require.NotNil(t, rule.OnError)
	assert.Equal(t, "match", *rule.OnError)

	flag := onOffFlag(rule)

	copied := flag.DeepCopy()
	require.NotNil(t, (*copied.Rules)[0].OnError)
	assert.Equal(t, "match", *(*copied.Rules)[0].OnError)
	assert.NotSame(t, rule.OnError, (*copied.Rules)[0].OnError)
}

func TestSegmentEvaluationWithConcurrentReloads(t *testing.T) {
	t.Parallel()

	svc := flagdef.NewService()
	provider := segment.NewMemoryProvider(map[string][]string{segBeta: {keyAlice}})

	build := func() *flagdef.InternalFlag {
		flag := onOffFlag(
			queryRule("n", `targetingKey in segment("beta")`),
			queryRule("j", `{"inSegment": [{"var": "targetingKey"}, "beta"]}`),
		)
		assert.NoError(t, flag.IsValid(svc))

		return flag
	}

	var current atomic.Pointer[flagdef.InternalFlag]

	current.Store(build())

	var wg sync.WaitGroup

	for range 4 {
		wg.Go(func() {
			flagCtx := flagdef.Context{Service: svc, Segments: provider}

			for range 200 {
				flag := current.Load()

				_, member := flag.Value("f", ffcontext.NewEvaluationContext(keyAlice), flagCtx)
				assert.Equal(t, variantOn, member.Variant)

				_, other := flag.Value("f", ffcontext.NewEvaluationContext(keyBob), flagCtx)
				assert.Equal(t, variantOff, other.Variant)
			}
		})
	}

	wg.Go(func() {
		for range 50 {
			current.Store(build())
		}
	})

	wg.Wait()
}

func TestSegmentTermsOfAFlag(t *testing.T) {
	t.Parallel()

	step := flagdef.ScheduledStep{Rules: &[]flagdef.Rule{queryRule("late", `key in segment("later")`)}}
	flag := onOffFlag(
		queryRule("a", `key in segment("zeta") or user.email in segment("alpha")`),
		queryRule("b", `{"inSegment": [{"var": "user.email"}, "alpha"]}`),
		queryRule("c", `key in segment("zeta")`),
	)
	flag.Scheduled = &[]flagdef.ScheduledStep{step}

	assert.Equal(t, []segment.Term{
		{Name: "alpha", Path: []string{ownedUserKey, "email"}},
		{Name: "later", Path: []string{"key"}},
		{Name: "zeta", Path: []string{"key"}},
	}, flag.SegmentTerms(flagdef.NewService()))
	assert.Empty(t, onOffFlag(queryRule("p", `age gt 1`)).SegmentTerms(flagdef.NewService()))
}

func TestSegmentDefaultRuleWithTheTextOfTheOperationIsAccepted(t *testing.T) {
	t.Parallel()

	flag := onOffFlag()
	flag.DefaultRule = &flagdef.Rule{
		Query:           new(`{"==": [{"var": "k"}, "inSegment"]}`),
		VariationResult: new(variantOff),
	}

	require.NoError(t, flag.IsValid(flagdef.NewService()))
}

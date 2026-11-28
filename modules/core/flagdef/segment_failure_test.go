package flagdef_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

const (
	secretID         = "secret-id-123"
	typeString       = "string"
	lookupXAlice     = "x:alice"
	lookupXBob       = "x:bob"
	ruleOnErrorMatch = "match"
	ruleOnErrorSkip  = "skip"
)

// recordingProvider answers like a MemoryProvider and records the lookups it receives.
type recordingProvider struct {
	inner segment.Provider
	calls [][]string
	mu    sync.Mutex
}

func (p *recordingProvider) Contains(ctx context.Context, lookups []segment.Lookup) error {
	ids := make([]string, 0, len(lookups))
	for _, lookup := range lookups {
		ids = append(ids, lookup.Segment+":"+lookup.ID)
	}

	p.mu.Lock()
	p.calls = append(p.calls, ids)
	p.mu.Unlock()

	return p.inner.Contains(ctx, lookups)
}

// invalidStateProvider answers every lookup with a state that is not one of the three constants.
type invalidStateProvider struct{}

func (invalidStateProvider) Contains(_ context.Context, lookups []segment.Lookup) error {
	for i := range lookups {
		lookups[i].State = segment.State(7)
	}

	return nil
}

// secretPanicProvider panics with a value that holds an identifier.
type secretPanicProvider struct{}

func (secretPanicProvider) Contains(context.Context, []segment.Lookup) error { panic(secretID) }

// oneLookup is the lookups that a provider receives for the rule of the matrix: one call with one lookup.
func oneLookup() [][]string { return [][]string{{"beta:alice"}} }

func TestSegmentFailureMatrix(t *testing.T) {
	t.Parallel()

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		provider   segment.Provider
		reqCtx     context.Context //nolint:containedctx // the table carries the request context of each case
		name       string
		wantCalls  [][]string
		wantStatus segment.Status
	}{
		{name: "panic", provider: panicProvider{}, wantStatus: segment.StatusStoreError, wantCalls: oneLookup()},
		{name: "error", provider: failingProvider{}, wantStatus: segment.StatusStoreError, wantCalls: oneLookup()},
		{name: "nil provider", provider: nil, wantStatus: segment.StatusNotConfigured, wantCalls: nil},
		{
			name:       "untouched lookup",
			provider:   silentProvider{},
			wantStatus: segment.StatusStoreError,
			wantCalls:  oneLookup(),
		},
		{
			name:       "invalid state",
			provider:   invalidStateProvider{},
			wantStatus: segment.StatusStoreError,
			wantCalls:  oneLookup(),
		},
		{
			name:     "cancelled context: no call",
			provider: segment.NewMemoryProvider(map[string][]string{segBeta: {keyAlice}}), reqCtx: cancelled,
			wantStatus: segment.StatusStoreError, wantCalls: nil,
		},
	}

	formats := map[string]string{formatNikunjy: nikunjyInclude, formatJSON: jsonInclude}
	ruleModes := map[string]*string{
		"unset":          nil,
		ruleOnErrorSkip:  new(ruleOnErrorSkip),
		ruleOnErrorMatch: new(ruleOnErrorMatch),
	}
	policies := []segment.OnError{segment.OnErrorUnknown, segment.OnErrorNotMember, segment.OnErrorMember}

	for _, tt := range tests {
		for format, query := range formats {
			for ruleMode, onError := range ruleModes {
				for _, policy := range policies {
					name := tt.name + "/" + format + "/rule " + ruleMode + "/segment " + string(policy)

					t.Run(name, func(t *testing.T) {
						t.Parallel()

						catalog := mustCatalog(
							t,
							segment.Definition{Name: segBeta, Type: segment.TypeValkey, OnError: policy},
						)
						svc := flagdef.NewService(flagdef.WithSegments(catalog))
						rule := queryRule("r", query)
						rule.OnError = onError
						flag := onOffFlag(rule)
						require.NoError(t, flag.IsValid(svc))

						var provider segment.Provider
						if tt.provider != nil {
							provider = &recordingProvider{inner: tt.provider}
						}

						_, details := flag.Value("f", ffcontext.NewEvaluationContext(keyAlice),
							flagdef.Context{Service: svc, Segments: provider, RequestContext: tt.reqCtx})

						// the segment policy resolves the unknown lookup first; if it stays unknown the rule decides
						ruleMatches := onError != nil && *onError == ruleOnErrorMatch
						wantOn := policy == segment.OnErrorMember || policy == segment.OnErrorUnknown && ruleMatches

						wantVariant, wantReason := variantOff, flagdef.ReasonDefault
						if wantOn {
							wantVariant, wantReason = variantOn, flagdef.ReasonTargetingMatch
						}

						assert.Equal(t, wantVariant, details.Variant)
						assert.Equal(t, wantReason, details.Reason)
						assert.Equal(t, tt.wantStatus, details.SegmentStatus)
						assert.False(t, details.Cacheable, "a value that could not read the store is not cacheable")

						if rec, ok := provider.(*recordingProvider); ok {
							assert.Equal(t, tt.wantCalls, rec.calls)
						}
					})
				}
			}
		}
	}
}

func TestSegmentProviderPanicIsLoggedByTypeOnly(t *testing.T) {
	t.Parallel()

	var buf syncBuffer

	svc := flagdef.NewService(flagdef.WithLogger(slog.New(slog.NewTextHandler(&buf, nil))))
	flag := onOffFlag(queryRule("r", nikunjyInclude))
	require.NoError(t, flag.IsValid(svc))

	_, details := flag.Value("f", ffcontext.NewEvaluationContext(keyAlice),
		flagdef.Context{Service: svc, Segments: secretPanicProvider{}})

	assert.Equal(t, segment.StatusStoreError, details.SegmentStatus)
	assert.Equal(t, 1, strings.Count(buf.String(), "segment lookup failed"), "logged once")
	assert.Contains(t, buf.String(), "panicked: "+typeString)
	assert.NotContains(t, buf.String(), secretID, "an identifier never reaches the log")
}

func TestSegmentEndedRequestContext(t *testing.T) {
	t.Parallel()

	expired, cancelExpired := context.WithDeadline(context.Background(), time.Unix(1, 0))
	t.Cleanup(cancelExpired)

	tests := []struct {
		reqCtx func() context.Context
		name   string
	}{
		{name: "cancelled", reqCtx: func() context.Context {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			return ctx
		}},
		{name: "deadline exceeded", reqCtx: func() context.Context { return expired }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf syncBuffer

			svc := flagdef.NewService(flagdef.WithLogger(slog.New(slog.NewTextHandler(&buf, nil))))
			rec := &recordingProvider{inner: segment.NewMemoryProvider(map[string][]string{segBeta: {keyAlice}})}
			flag := onOffFlag(queryRule("r", nikunjyInclude))
			require.NoError(t, flag.IsValid(svc))

			_, details := flag.Value("f", ffcontext.NewEvaluationContext(keyAlice),
				flagdef.Context{Service: svc, Segments: rec, RequestContext: tt.reqCtx()})

			assert.Empty(t, rec.calls, "the provider is not called for a request that has ended")
			assert.Empty(t, buf.String(), "a request that ended is not a failure of the store: no line")
			assert.Equal(t, variantOff, details.Variant)
			assert.Equal(t, segment.StatusStoreError, details.SegmentStatus)
			assert.False(t, details.Cacheable)
		})
	}
}

// memoryAnswers is a provider that answers from memory, whatever the state of the request.
type memoryAnswers struct{}

func (memoryAnswers) AnswersFromMemory() {}

// Contains answers every lookup with a member, without looking at the context.
func (memoryAnswers) Contains(_ context.Context, lookups []segment.Lookup) error {
	for i := range lookups {
		lookups[i].State = segment.Member
	}

	return nil
}

// A provider that answers from memory is called for a request that has ended and its answers are used; a provider
// that fails is logged once.
func TestSegmentEndedRequestContextWithAnswersInMemory(t *testing.T) {
	t.Parallel()

	tests := []struct {
		provider     segment.Provider
		name         string
		wantVariant  string
		wantLogLines int
		wantStatus   segment.Status
		ended        bool
	}{
		{
			name:        "answers in memory, request ended",
			provider:    memoryAnswers{},
			ended:       true,
			wantVariant: variantOn,
			wantStatus:  segment.StatusHealthy,
		},
		{
			name: "failing store, request alive", provider: failingProvider{},
			wantVariant: variantOff, wantStatus: segment.StatusStoreError, wantLogLines: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reqCtx, cancel := context.WithCancel(t.Context())
			defer cancel()

			if tt.ended {
				cancel()
			}

			var buf syncBuffer

			svc := flagdef.NewService(flagdef.WithLogger(slog.New(slog.NewTextHandler(&buf, nil))))
			flag := onOffFlag(queryRule("r", nikunjyInclude))
			require.NoError(t, flag.IsValid(svc))

			_, details := flag.Value("f", ffcontext.NewEvaluationContext(keyAlice),
				flagdef.Context{Service: svc, Segments: tt.provider, RequestContext: reqCtx})

			assert.Equal(t, tt.wantVariant, details.Variant)
			assert.Equal(t, tt.wantStatus, details.SegmentStatus)
			assert.Equal(t, tt.wantLogLines, strings.Count(buf.String(), "segment lookup failed"))
		})
	}
}

func TestSegmentAttributeIDByType(t *testing.T) {
	t.Parallel()

	const bigFloat = float64(1 << 60)

	tests := []struct {
		attr   any
		name   string
		wantID string // empty: not an identifier, no lookup
	}{
		{name: "string", attr: "abc", wantID: "abc"},
		{name: "empty string", attr: "", wantID: ""},
		{name: "int", attr: 42, wantID: "42"},
		{name: "negative int", attr: -5, wantID: "-5"},
		{name: "int8", attr: int8(-8), wantID: "-8"},
		{name: "int16", attr: int16(16), wantID: "16"},
		{name: "int32", attr: int32(32), wantID: "32"},
		{name: "int64", attr: int64(math.MinInt64), wantID: "-9223372036854775808"},
		{name: "uint", attr: uint(7), wantID: "7"},
		{name: "uint8", attr: uint8(200), wantID: "200"},
		{name: "uint16", attr: uint16(60000), wantID: "60000"},
		{name: "uint32", attr: uint32(4000000000), wantID: "4000000000"},
		{name: "uint64", attr: uint64(math.MaxUint64), wantID: "18446744073709551615"},
		{name: "float64 whole", attr: float64(42), wantID: "42"},
		{name: "float64 whole, large", attr: 1e15, wantID: "1000000000000000"},
		{name: "float64 beyond 2^53", attr: bigFloat, wantID: "1152921504606846976"},
		{name: "float64 negative whole", attr: float64(-3), wantID: "-3"},
		{name: "float64 negative zero", attr: math.Copysign(0, -1), wantID: "0"},
		{name: "float32 whole", attr: float32(42), wantID: "42"},
		{name: "float64 fraction", attr: 42.5, wantID: ""},
		{name: "float32 fraction", attr: float32(0.5), wantID: ""},
		{name: "NaN", attr: math.NaN(), wantID: ""},
		{name: "+Inf", attr: math.Inf(1), wantID: ""},
		{name: "-Inf", attr: math.Inf(-1), wantID: ""},
		{name: "json.Number integer", attr: json.Number("42"), wantID: "42"},
		{name: "json.Number big integer", attr: json.Number("9223372036854775807"), wantID: "9223372036854775807"},
		{name: "json.Number whole float", attr: json.Number("42.0"), wantID: "42"},
		{name: "json.Number exponent", attr: json.Number("4.2e1"), wantID: "42"},
		{name: "json.Number fraction", attr: json.Number("42.5"), wantID: ""},
		{name: "json.Number garbage", attr: json.Number("abc"), wantID: ""},
		{name: "bool", attr: true, wantID: ""},
		{name: "map", attr: map[string]any{"id": "x"}, wantID: ""},
		{name: "slice", attr: []any{"x"}, wantID: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := flagdef.NewService()
			rec := &recordingProvider{inner: segment.NewMemoryProvider(map[string][]string{segIDs: {tt.wantID}})}
			flag := onOffFlag(queryRule("r", `uid in segment("ids")`))
			require.NoError(t, flag.IsValid(svc))

			evalCtx := ffcontext.NewEvaluationContextBuilder("k").AddCustom("uid", tt.attr).Build()
			_, details := flag.Value("f", evalCtx, flagdef.Context{Service: svc, Segments: rec})

			if tt.wantID == "" {
				assert.Empty(t, rec.calls, "no lookup for what is not an identifier")
				assert.Equal(t, variantOff, details.Variant)

				return
			}

			assert.Equal(t, [][]string{{"ids:" + tt.wantID}}, rec.calls)
			assert.Equal(t, variantOn, details.Variant)
		})
	}
}

func TestSegmentExclusionOfAFloatIdentifier(t *testing.T) {
	t.Parallel()

	svc := flagdef.NewService()
	provider := segment.NewMemoryProvider(map[string][]string{"blocked": {"42"}})
	flag := onOffFlag(queryRule("r", `not (uid in segment("blocked"))`))
	require.NoError(t, flag.IsValid(svc))

	tests := []struct {
		name        string
		uid         any
		wantVariant string
	}{
		{name: "a float64 id that is a member is excluded", uid: float64(42), wantVariant: variantOff},
		{name: "a string id that is a member is excluded", uid: "42", wantVariant: variantOff},
		{name: "another float64 id is allowed", uid: float64(43), wantVariant: variantOn},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			evalCtx := ffcontext.NewEvaluationContextBuilder("k").AddCustom("uid", tt.uid).Build()
			_, details := flag.Value("f", evalCtx, flagdef.Context{Service: svc, Segments: provider})
			assert.Equal(t, tt.wantVariant, details.Variant)
		})
	}
}

// The order of the operands decides what is looked up: the rule is evaluated left to right, like a rule without
// segments, until it reaches a segment term. Put the cheap conditions first.
func TestSegmentLookupsFollowTheOrderOfTheOperands(t *testing.T) {
	t.Parallel()

	members := map[string][]string{"x": {keyAlice}, "y": {keyAlice}, "z": {keyBob}}

	tests := []struct {
		name          string
		nikunjy       string
		json          string
		plan          string
		key           string
		wantVariant   string
		wantLookups   [][]string
		wantCacheable bool
		wantStatus    segment.Status
	}{
		{
			name:          "false and a segment: decided, nothing is looked up",
			nikunjy:       `plan eq "free" and targetingKey in segment("x")`,
			json:          `{"and": [{"==": [{"var": "plan"}, "free"]}, {"inSegment": [{"var": "targetingKey"}, "x"]}]}`,
			plan:          planPro,
			key:           keyAlice,
			wantVariant:   variantOff,
			wantStatus:    segment.StatusNone,
			wantCacheable: true,
		},
		{
			name:    "true or a segment: decided, nothing is looked up",
			nikunjy: `plan eq "pro" or targetingKey in segment("x")`,
			json:    `{"or": [{"==": [{"var": "plan"}, "pro"]}, {"inSegment": [{"var": "targetingKey"}, "x"]}]}`,
			plan:    planPro, key: keyBob, wantVariant: variantOn, wantStatus: segment.StatusNone, wantCacheable: true,
		},
		{
			name:    "true and a segment: the segment is reached",
			nikunjy: `plan eq "pro" and targetingKey in segment("x")`,
			json:    `{"and": [{"==": [{"var": "plan"}, "pro"]}, {"inSegment": [{"var": "targetingKey"}, "x"]}]}`,
			plan:    planPro, key: keyAlice, wantLookups: [][]string{{lookupXAlice}}, wantVariant: variantOn,
			wantStatus: segment.StatusHealthy,
		},
		{
			name:    "a segment or true: the segment comes first, so it is looked up",
			nikunjy: `targetingKey in segment("x") or plan eq "pro"`,
			json:    `{"or": [{"inSegment": [{"var": "targetingKey"}, "x"]}, {"==": [{"var": "plan"}, "pro"]}]}`,
			plan:    planPro, key: keyBob, wantLookups: [][]string{{lookupXBob}}, wantVariant: variantOn,
			wantStatus: segment.StatusHealthy,
		},
		{
			name:    "mixed: reaching a term looks every term up",
			nikunjy: `(plan eq "pro" or targetingKey in segment("x")) and targetingKey in segment("y")`,
			json: `{"and": [{"or": [{"==": [{"var": "plan"}, "pro"]}, {"inSegment": [{"var": "targetingKey"}, "x"]}]}, ` +
				`{"inSegment": [{"var": "targetingKey"}, "y"]}]}`,
			plan:        "pro",
			key:         keyAlice,
			wantLookups: [][]string{{lookupXAlice, "y:alice"}},
			wantVariant: variantOn,
			wantStatus:  segment.StatusHealthy,
		},
		{
			name:    "nested not: a decided side before the segment",
			nikunjy: `not (plan eq "pro" or targetingKey in segment("x")) or targetingKey in segment("z")`,
			json: `{"or": [{"!": {"or": [{"==": [{"var": "plan"}, "pro"]}, {"inSegment": [{"var": "targetingKey"}, "x"]}]}}, ` +
				`{"inSegment": [{"var": "targetingKey"}, "z"]}]}`,
			plan:        "pro",
			key:         keyBob,
			wantLookups: [][]string{{lookupXBob, "z:bob"}},
			wantVariant: variantOn,
			wantStatus:  segment.StatusHealthy,
		},
		{
			name:    "both terms in one call",
			nikunjy: `targetingKey in segment("x") or targetingKey in segment("z")`,
			json: `{"or": [{"inSegment": [{"var": "targetingKey"}, "x"]}, ` +
				`{"inSegment": [{"var": "targetingKey"}, "z"]}]}`,
			plan: planPro, key: keyBob, wantLookups: [][]string{{lookupXBob, "z:bob"}}, wantVariant: variantOn,
			wantStatus: segment.StatusHealthy,
		},
		{
			name:    "a term without an identifier is not looked up, the others are",
			nikunjy: `targetingKey in segment("x") and missing in segment("y")`,
			json: `{"and": [{"inSegment": [{"var": "targetingKey"}, "x"]}, ` +
				`{"inSegment": [{"var": "missing"}, "y"]}]}`,
			plan: planPro, key: keyAlice, wantLookups: [][]string{{lookupXAlice}}, wantVariant: variantOff,
			wantStatus: segment.StatusHealthy,
		},
		{
			name:    "no term has an identifier: nothing is looked up",
			nikunjy: `missing in segment("x") or other in segment("y")`,
			json: `{"or": [{"inSegment": [{"var": "missing"}, "x"]}, ` +
				`{"inSegment": [{"var": "other"}, "y"]}]}`,
			plan: planPro, key: keyAlice, wantVariant: variantOff, wantStatus: segment.StatusNone, wantCacheable: true,
		},
	}

	for _, tt := range tests {
		for format, query := range map[string]string{formatNikunjy: tt.nikunjy, formatJSON: tt.json} {
			t.Run(tt.name+"/"+format, func(t *testing.T) {
				t.Parallel()

				svc := flagdef.NewService()
				rec := &recordingProvider{inner: segment.NewMemoryProvider(members)}
				flag := onOffFlag(queryRule("r", query))
				require.NoError(t, flag.IsValid(svc))

				evalCtx := ffcontext.NewEvaluationContextBuilder(tt.key).AddCustom("plan", tt.plan).Build()
				_, details := flag.Value("f", evalCtx, flagdef.Context{Service: svc, Segments: rec})

				assert.Equal(t, tt.wantLookups, rec.calls)
				assert.Equal(t, tt.wantVariant, details.Variant)
				assert.Equal(t, tt.wantStatus, details.SegmentStatus)
				assert.Equal(t, tt.wantCacheable, details.Cacheable)
			})
		}
	}
}

// answersThenFails answers the lookups of the segment "a" like a MemoryProvider and fails: the others stay unknown.
type answersThenFails struct{ inner segment.Provider }

func (p answersThenFails) Contains(ctx context.Context, lookups []segment.Lookup) error {
	answered := make([]segment.Lookup, 0, len(lookups))

	for _, lookup := range lookups {
		if lookup.Segment == "a" {
			answered = append(answered, lookup)
		}
	}

	_ = p.inner.Contains(ctx, answered)

	next := 0

	for i := range lookups {
		if lookups[i].Segment == "a" {
			lookups[i].State = answered[next].State
			next++
		}
	}

	return errStoreDown
}

// answersThenPanics sets every state to Member and panics: a provider that panics is not trusted.
type answersThenPanics struct{}

func (answersThenPanics) Contains(_ context.Context, lookups []segment.Lookup) error {
	for i := range lookups {
		lookups[i].State = segment.Member
	}

	panic(secretID)
}

// A provider that fails after it answered some lookups: the answers it gave are kept, the others are unknown, and the
// rule is decided with Kleene's logic, so an answer that decides the rule still decides it.
func TestSegmentProviderErrorKeepsTheAnswersItGave(t *testing.T) {
	t.Parallel()

	const (
		rulesOr  = `targetingKey in segment("a") or targetingKey in segment("b")`
		rulesAnd = `targetingKey in segment("a") and targetingKey in segment("b")`
		jsonOr   = `{"or": [{"inSegment": [{"var": "targetingKey"}, "a"]}, {"inSegment": [{"var": "targetingKey"}, "b"]}]}`
		jsonAnd  = `{"and": [{"inSegment": [{"var": "targetingKey"}, "a"]}, {"inSegment": [{"var": "targetingKey"}, "b"]}]}`
	)

	tests := []struct {
		name        string
		query       string
		onError     *string
		provider    segment.Provider
		key         string
		wantVariant string
		defaults    bool // the default policy: what the provider cannot answer is not a member, onError has nothing to decide
	}{
		{name: "or: a member of a decides", query: rulesOr, key: keyAlice, wantVariant: variantOn},
		{name: "or: a member of a decides, json", query: jsonOr, key: keyAlice, wantVariant: variantOn},
		{name: "and: a non member of a decides", query: rulesAnd, key: keyBob, wantVariant: variantOff},
		{
			name: "and: a non member of a decides over onError match", query: rulesAnd, key: keyBob,
			onError: new(ruleOnErrorMatch), wantVariant: variantOff,
		},
		{
			name: "and: a non member of a decides over onError match, json", query: jsonAnd, key: keyBob,
			onError: new(ruleOnErrorMatch), wantVariant: variantOff,
		},
		{
			name:        "and: a member of a leaves b unknown, the rule is skipped",
			query:       rulesAnd,
			key:         keyAlice,
			wantVariant: variantOff,
		},
		{
			name: "and: a member of a leaves b unknown, onError match", query: rulesAnd, key: keyAlice,
			onError: new(ruleOnErrorMatch), wantVariant: variantOn,
		},
		{
			name: "and: a member of a leaves b unknown, onError match, json", query: jsonAnd, key: keyAlice,
			onError: new(ruleOnErrorMatch), wantVariant: variantOn,
		},
		{
			name:        "or: a non member of a leaves b unknown, the rule is skipped",
			query:       rulesOr,
			key:         keyBob,
			wantVariant: variantOff,
		},
		{
			name: "or: a non member of a leaves b unknown, onError match", query: jsonOr, key: keyBob,
			onError: new(ruleOnErrorMatch), wantVariant: variantOn,
		},
		{
			name:  "default policy: a member of a leaves b not a member, onError match has nothing to decide",
			query: rulesAnd, key: keyAlice, onError: new(ruleOnErrorMatch), defaults: true, wantVariant: variantOff,
		},
		{
			name:  "default policy: a non member of a leaves b not a member, onError match has nothing to decide, json",
			query: jsonOr, key: keyBob, onError: new(ruleOnErrorMatch), defaults: true, wantVariant: variantOff,
		},
		{
			name:  "default policy: not (a member of a, b not a member) matches while the store is down",
			query: `not (targetingKey in segment("b"))`, key: keyAlice, defaults: true, wantVariant: variantOn,
		},
		{
			name: "a provider that panics after answering is not trusted", query: rulesOr, key: keyAlice,
			provider: answersThenPanics{}, wantVariant: variantOff,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			provider := tt.provider
			if provider == nil {
				provider = answersThenFails{inner: segment.NewMemoryProvider(map[string][]string{"a": {keyAlice}})}
			}

			var opts []flagdef.ServiceOption
			if tt.onError != nil && !tt.defaults {
				opts = append(
					opts,
					flagdef.WithSegments(optInCatalog(t, "a", "b")),
				) // the three-valued path is an opt-in
			}

			svc := flagdef.NewService(opts...)
			rule := queryRule("r", tt.query)
			rule.OnError = tt.onError
			flag := onOffFlag(rule)
			require.NoError(t, flag.IsValid(svc))

			_, details := flag.Value(
				"f",
				ffcontext.NewEvaluationContext(tt.key),
				flagdef.Context{Service: svc, Segments: provider},
			)

			assert.Equal(t, tt.wantVariant, details.Variant)
			assert.Equal(t, segment.StatusStoreError, details.SegmentStatus)
			assert.False(t, details.Cacheable)
		})
	}
}

// cancelsAfterAnswering answers the lookups of the segment "a" and then the request ends during the call.
type cancelsAfterAnswering struct {
	cancel context.CancelFunc
	inner  segment.Provider
}

func (p cancelsAfterAnswering) Contains(ctx context.Context, lookups []segment.Lookup) error {
	answered := answersThenFails{inner: p.inner}.Contains(ctx, lookups) // sets the answers of "a", reports an error

	p.cancel()

	if answered == nil {
		return nil
	}

	return fmt.Errorf("the request ended: %w", ctx.Err())
}

// The request ends during the call: like any error of the provider, the answers that it gave before keep their value.
func TestSegmentRequestEndingDuringTheCallKeepsTheAnswersGiven(t *testing.T) {
	t.Parallel()

	const (
		rulesOr  = `targetingKey in segment("a") or targetingKey in segment("b")`
		rulesAnd = `targetingKey in segment("a") and targetingKey in segment("b")`
		jsonOr   = `{"or": [{"inSegment": [{"var": "targetingKey"}, "a"]}, {"inSegment": [{"var": "targetingKey"}, "b"]}]}`
		jsonAnd  = `{"and": [{"inSegment": [{"var": "targetingKey"}, "a"]}, {"inSegment": [{"var": "targetingKey"}, "b"]}]}`
	)

	tests := []struct {
		name        string
		query       string
		key         string
		wantVariant string
	}{
		{name: "rules or: a member of a decides", query: rulesOr, key: keyAlice, wantVariant: variantOn},
		{name: "json or: a member of a decides", query: jsonOr, key: keyAlice, wantVariant: variantOn},
		{name: "rules and: a non member of a decides", query: rulesAnd, key: keyBob, wantVariant: variantOff},
		{name: "json and: a non member of a decides", query: jsonAnd, key: keyBob, wantVariant: variantOff},
		{
			name:        "rules and: b stays unknown, the rule is skipped",
			query:       rulesAnd,
			key:         keyAlice,
			wantVariant: variantOff,
		},
		{name: "json or: b stays unknown, the rule is skipped", query: jsonOr, key: keyBob, wantVariant: variantOff},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)

			provider := cancelsAfterAnswering{
				cancel: cancel,
				inner:  segment.NewMemoryProvider(map[string][]string{"a": {keyAlice}}),
			}
			svc := flagdef.NewService()
			flag := onOffFlag(queryRule("r", tt.query))
			require.NoError(t, flag.IsValid(svc))

			_, details := flag.Value("f", ffcontext.NewEvaluationContext(tt.key),
				flagdef.Context{Service: svc, Segments: provider, RequestContext: ctx})

			assert.Equal(t, tt.wantVariant, details.Variant)
			assert.Equal(t, segment.StatusStoreError, details.SegmentStatus)
			assert.False(t, details.Cacheable)
			require.ErrorIs(t, ctx.Err(), context.Canceled)
		})
	}
}

package flagdef_test

import (
	"context"
	"encoding/json"
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

func TestSegmentFailureMatrix(t *testing.T) {
	t.Parallel()

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		provider   segment.Provider
		reqCtx     context.Context //nolint:containedctx // the table carries the request context of each case
		name       string
		wantStatus segment.Status
		wantCalls  int
	}{
		{name: "panic", provider: panicProvider{}, wantStatus: segment.StatusStoreError, wantCalls: 1},
		{name: "error", provider: failingProvider{}, wantStatus: segment.StatusStoreError, wantCalls: 1},
		{name: "nil provider", provider: nil, wantStatus: segment.StatusNotConfigured, wantCalls: 0},
		{name: "untouched lookup", provider: silentProvider{}, wantStatus: segment.StatusStoreError, wantCalls: 1},
		{name: "invalid state", provider: invalidStateProvider{}, wantStatus: segment.StatusStoreError, wantCalls: 1},
		{
			name:     "cancelled context: no call",
			provider: segment.NewMemoryProvider(map[string][]string{segBeta: {keyAlice}}), reqCtx: cancelled,
			wantStatus: segment.StatusStoreError, wantCalls: 0,
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
							assert.Len(t, rec.calls, tt.wantCalls)
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

			svc := flagdef.NewService()
			rec := &recordingProvider{inner: segment.NewMemoryProvider(map[string][]string{segBeta: {keyAlice}})}
			flag := onOffFlag(queryRule("r", nikunjyInclude))
			require.NoError(t, flag.IsValid(svc))

			_, details := flag.Value("f", ffcontext.NewEvaluationContext(keyAlice),
				flagdef.Context{Service: svc, Segments: rec, RequestContext: tt.reqCtx()})

			assert.Empty(t, rec.calls, "the provider is not called for a request that has ended")
			assert.Equal(t, variantOff, details.Variant)
			assert.Equal(t, segment.StatusStoreError, details.SegmentStatus)
			assert.False(t, details.Cacheable)
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
			rec := &recordingProvider{inner: segment.NewMemoryProvider(map[string][]string{"ids": {tt.wantID}})}
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

func TestSegmentOnlyTheTermsThatMatterAreLookedUp(t *testing.T) {
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
			name:          "false and a segment: decided without a lookup",
			nikunjy:       `plan eq "free" and targetingKey in segment("x")`,
			json:          `{"and": [{"==": [{"var": "plan"}, "free"]}, {"inSegment": [{"var": "targetingKey"}, "x"]}]}`,
			plan:          planPro,
			key:           keyAlice,
			wantVariant:   variantOff,
			wantStatus:    segment.StatusNone,
			wantCacheable: true,
		},
		{
			name:    "true or a segment: decided without a lookup",
			nikunjy: `plan eq "pro" or targetingKey in segment("x")`,
			json:    `{"or": [{"==": [{"var": "plan"}, "pro"]}, {"inSegment": [{"var": "targetingKey"}, "x"]}]}`,
			plan:    planPro, key: keyBob, wantVariant: variantOn, wantStatus: segment.StatusNone, wantCacheable: true,
		},
		{
			name:    "true and a segment: the segment is needed",
			nikunjy: `plan eq "pro" and targetingKey in segment("x")`,
			json:    `{"and": [{"==": [{"var": "plan"}, "pro"]}, {"inSegment": [{"var": "targetingKey"}, "x"]}]}`,
			plan:    planPro, key: keyAlice, wantLookups: [][]string{{"x:alice"}}, wantVariant: variantOn,
			wantStatus: segment.StatusHealthy,
		},
		{
			name:    "mixed: only the term that is still needed",
			nikunjy: `(plan eq "pro" or targetingKey in segment("x")) and targetingKey in segment("y")`,
			json: `{"and": [{"or": [{"==": [{"var": "plan"}, "pro"]}, {"inSegment": [{"var": "targetingKey"}, "x"]}]}, ` +
				`{"inSegment": [{"var": "targetingKey"}, "y"]}]}`,
			plan:        planPro,
			key:         keyAlice,
			wantLookups: [][]string{{"y:alice"}},
			wantVariant: variantOn,
			wantStatus:  segment.StatusHealthy,
		},
		{
			name:    "nested not: the decided side is not looked up",
			nikunjy: `not (plan eq "pro" or targetingKey in segment("x")) or targetingKey in segment("z")`,
			json: `{"or": [{"!": {"or": [{"==": [{"var": "plan"}, "pro"]}, {"inSegment": [{"var": "targetingKey"}, "x"]}]}}, ` +
				`{"inSegment": [{"var": "targetingKey"}, "z"]}]}`,
			plan:        planPro,
			key:         keyBob,
			wantLookups: [][]string{{"z:bob"}},
			wantVariant: variantOn,
			wantStatus:  segment.StatusHealthy,
		},
		{
			name:    "both terms unknown: both are sent in one call",
			nikunjy: `targetingKey in segment("x") or targetingKey in segment("z")`,
			json: `{"or": [{"inSegment": [{"var": "targetingKey"}, "x"]}, ` +
				`{"inSegment": [{"var": "targetingKey"}, "z"]}]}`,
			plan: planPro, key: keyBob, wantLookups: [][]string{{"x:bob", "z:bob"}}, wantVariant: variantOn,
			wantStatus: segment.StatusHealthy,
		},
		{
			name:    "a missing attribute decides without a lookup",
			nikunjy: `targetingKey in segment("x") and missing in segment("y")`,
			json: `{"and": [{"inSegment": [{"var": "targetingKey"}, "x"]}, ` +
				`{"inSegment": [{"var": "missing"}, "y"]}]}`,
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

package flagruntime_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/resolver"
)

const (
	// leaveBound is how long a test waits for a request that must leave at once: a failure bound, never a pause.
	leaveBound = 10 * time.Second
	// longCall is a call timeout that the tests never reach: only the context of a request can end a wait early.
	longCall = time.Hour

	flagAudience = "audience-flag"
)

// holdStore is a segment store that holds every call until the test opens it, or until the context of the call ends.
type holdStore struct {
	inner   *segment.MemorySource
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	calls   atomic.Int32
}

func newHoldStore(t *testing.T) *holdStore {
	t.Helper()

	store := &holdStore{
		inner:   segment.NewMemorySource(map[string][]string{audienceSegmentName: {memberAlice}}),
		entered: make(chan struct{}, 64),
		release: make(chan struct{}),
	}

	t.Cleanup(store.open)

	return store
}

// Contains implements [segment.Provider].
func (g *holdStore) Contains(ctx context.Context, lookups []segment.Lookup) error {
	g.calls.Add(1)

	g.entered <- struct{}{}

	select {
	case <-g.release:
		return g.inner.Contains(ctx, lookups)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// open lets the calls answer.
func (g *holdStore) open() { g.once.Do(func() { close(g.release) }) }

type holdBinder struct{ store *holdStore }

func (b holdBinder) Bind(
	connection string, _ segment.MissingKey, _ []flagruntime.BoundDefinition,
) (*flagruntime.Binding, error) {
	if connection != connAudiences {
		return nil, errNoConnection
	}

	return &flagruntime.Binding{Provider: b.store, Versioner: b.store.inner, Source: b.store.inner}, nil
}

// ctxRig is a flag set whose dynamic segment is served by a gated store.
type ctxRig struct {
	store    *holdStore
	counters *resolver.Counters
	eval     *flagruntime.Evaluator
	built    *flagruntime.Built
	set      *flagruntime.FlagSet
}

func newCtxRig(t *testing.T, traced bool) *ctxRig {
	t.Helper()

	failures := 1
	callTimeout := bootstrap.Duration(longCall)
	rig := &ctxRig{store: newHoldStore(t), counters: &resolver.Counters{}}
	builder := viewBuilderWith(t, &flagruntime.SegmentEnv{
		Binder:   holdBinder{store: rig.store},
		Observer: rig.counters,
		Traced:   traced,
		Resolver: &bootstrap.SegmentResolver{
			CallTimeout: &callTimeout,
			Default:     bootstrap.SegmentTuning{BreakerFailures: &failures},
		},
	})

	built, err := builder.Build(t.Context(), viewContent(t, lookupDoc))
	require.NoError(t, err)

	builder.Commit(built)

	rig.built = built
	rig.set = built.Runtime.FlagSets["a"]
	rig.eval = flagruntime.NewEvaluator(newService(t), nil)

	return rig
}

// evaluate runs one evaluation on its own goroutine and returns the channel of its result.
func evaluate(ctx context.Context, run func(context.Context) segment.Status) <-chan segment.Status {
	out := make(chan segment.Status, 1)

	go func() { out <- run(ctx) }()

	return out
}

// awaitStatus returns the result of an evaluation that must leave while the store is still holding its call.
func awaitStatus(t *testing.T, out <-chan segment.Status) segment.Status {
	t.Helper()

	select {
	case status := <-out:
		return status
	case <-time.After(leaveBound):
		require.FailNow(t, "the evaluation was held past the end of its request context")
	}

	return segment.StatusNone
}

type evalEndpoint struct {
	run  func(context.Context, *ctxRig) segment.Status
	name string
	// wantStatus is the status of the unanswered lookup. A typed call of another type than the flag answers its default
	// without a segment status, but it asked the store all the same.
	wantStatus segment.Status
}

func evalEndpoints() []evalEndpoint {
	target := ffcontext.NewEvaluationContext(memberAlice)

	return []evalEndpoint{
		{
			name: "raw variation (OFREP single, eval)", wantStatus: segment.StatusStoreError,
			run: func(ctx context.Context, r *ctxRig) segment.Status {
				res, _ := r.eval.RawVariation(ctx, r.set, flagAudience, target, nil)

				return res.SegmentStatus
			}},
		{
			name: "bool variation", wantStatus: segment.StatusStoreError,
			run: func(ctx context.Context, r *ctxRig) segment.Status {
				res, _ := r.eval.BoolVariationDetails(ctx, r.set, flagAudience, target, false)

				return res.SegmentStatus
			}},
		{
			name: "int variation", wantStatus: segment.StatusNone,
			run: func(ctx context.Context, r *ctxRig) segment.Status {
				res, _ := r.eval.IntVariationDetails(ctx, r.set, flagAudience, target, 0)

				return res.SegmentStatus
			}},
		{
			name: "float variation", wantStatus: segment.StatusNone,
			run: func(ctx context.Context, r *ctxRig) segment.Status {
				res, _ := r.eval.Float64VariationDetails(ctx, r.set, flagAudience, target, 0)

				return res.SegmentStatus
			}},
		{
			name: "string variation", wantStatus: segment.StatusNone,
			run: func(ctx context.Context, r *ctxRig) segment.Status {
				res, _ := r.eval.StringVariationDetails(ctx, r.set, flagAudience, target, "")

				return res.SegmentStatus
			}},
		{
			name: "json variation", wantStatus: segment.StatusNone,
			run: func(ctx context.Context, r *ctxRig) segment.Status {
				res, _ := r.eval.JSONVariationDetails(ctx, r.set, flagAudience, target, nil)

				return res.SegmentStatus
			}},
		{
			name: "json array variation", wantStatus: segment.StatusNone,
			run: func(ctx context.Context, r *ctxRig) segment.Status {
				res, _ := r.eval.JSONArrayVariationDetails(ctx, r.set, flagAudience, target, nil)

				return res.SegmentStatus
			}},
		{
			name: "all flags (OFREP bulk, allflags)", wantStatus: segment.StatusStoreError,
			run: func(ctx context.Context, r *ctxRig) segment.Status {
				states := r.eval.AllFlagsState(ctx, r.set, target)

				return states.GetFlags()[flagAudience].SegmentStatus
			}},
		{
			name: "listed flags (OFREP bulk with a flag list)", wantStatus: segment.StatusStoreError,
			run: func(ctx context.Context, r *ctxRig) segment.Status {
				states := r.eval.GetFlagStates(ctx, r.set, target, []string{flagAudience})

				return states.GetFlags()[flagAudience].SegmentStatus
			}},
	}
}

// The context of the request reaches the segment lookups of every evaluation: a request that ends while the store
// holds its call leaves at once, its lookup is unanswered (STORE_ERROR), and the call of the store is not its own.
func TestRequestContextEndsTheSegmentLookups(t *testing.T) {
	t.Parallel()

	for _, tt := range evalEndpoints() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rig := newCtxRig(t, false)
			ctx, cancel := context.WithCancel(t.Context())
			out := evaluate(ctx, func(ctx context.Context) segment.Status { return tt.run(ctx, rig) })

			<-rig.store.entered // the lookup is in the store, which holds it
			cancel()

			assert.Equal(t, tt.wantStatus, awaitStatus(t, out))
			assert.Equal(t, int32(1), rig.store.calls.Load())
		})
	}
}

// A request that is cancelled is not a failure of the store: the breaker, the failure count and the health of the
// connection stay as they were, and the call that the requests started goes on and answers the next one.
func TestCancelledRequestsAreNotStoreFailures(t *testing.T) {
	t.Parallel()

	const requests = 5

	tests := []struct {
		name            string
		before          bool
		wantStoreCalled int32
	}{
		{name: "cancelled while the store holds the call", wantStoreCalled: 1},
		{name: "cancelled before the evaluation", before: true, wantStoreCalled: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rig := newCtxRig(t, false)
			target := ffcontext.NewEvaluationContext(memberAlice)

			for range requests {
				ctx, cancel := context.WithCancel(t.Context())
				if tt.before {
					cancel()
				}

				out := evaluate(ctx, func(ctx context.Context) segment.Status {
					res, _ := rig.eval.RawVariation(ctx, rig.set, flagAudience, target, nil)

					return res.SegmentStatus
				})

				if !tt.before && rig.store.calls.Load() == 0 {
					<-rig.store.entered
				}

				cancel()

				assert.Equal(t, segment.StatusStoreError, awaitStatus(t, out))
			}

			healthy, failing := rig.set.Segments.View.Health()

			assert.Equal(t, []flagruntime.ConnectionBreakers{{Connection: connAudiences}},
				rig.built.Runtime.SegmentBindings().Breakers)
			assert.Equal(t, 1, healthy)
			assert.Equal(t, 0, failing)
			assert.Equal(t, uint64(0), rig.counters.Snapshot().Failed)
			assert.Equal(t, tt.wantStoreCalled, rig.store.calls.Load())
		})
	}
}

// Requests that ask the same question share one call to the store, and each leaves on its own context: the one that is
// cancelled gets no answer, the other still gets the answer of the call that goes on.
func TestCancelledRequestDoesNotCancelTheSharedCall(t *testing.T) {
	t.Parallel()

	rig := newCtxRig(t, false)
	target := ffcontext.NewEvaluationContext(memberAlice)
	run := func(ctx context.Context) segment.Status {
		res, _ := rig.eval.BoolVariationDetails(ctx, rig.set, flagAudience, target, false)
		if res.Value {
			return segment.StatusHealthy
		}

		return res.SegmentStatus
	}

	cancelled, cancel := context.WithCancel(t.Context())
	first := evaluate(cancelled, run)

	<-rig.store.entered

	second := evaluate(t.Context(), run)

	cancel()

	assert.Equal(t, segment.StatusStoreError, awaitStatus(t, first), "the cancelled request has no answer")

	rig.store.open()

	assert.Equal(t, segment.StatusHealthy, awaitStatus(t, second), "the other request gets the member answer")
	assert.Equal(t, int32(1), rig.store.calls.Load(), "one call for both requests")
}

// The lookups are a span of the request: the child of the span that the context carries, in its trace, and recorded
// when the request is sampled even though the sampler is the default one (parent based, root never).
func TestSegmentLookupsSpanIsAChildOfTheRequest(t *testing.T) {
	// the tracer provider is global: this test cannot run in parallel with the others that set it
	t.Setenv("SEMAPHORE_TEST_SERIAL", "tracer-provider")

	traceID := trace.TraceID{1, 2, 3}
	requestSpan := trace.SpanID{4, 5, 6}

	tests := []requestSpanCase{
		{name: "sampled request", flags: trace.FlagsSampled, wantSpans: 1, wantParent: requestSpan, wantTrace: traceID},
		{name: "request not sampled", flags: 0, wantSpans: 0},
		{name: "no request span", noRequest: true, wantSpans: 0},
	}

	for i := range tests {
		checkRequestSpan(t, &tests[i])
	}
}

type requestSpanCase struct {
	name       string
	wantParent trace.SpanID
	wantTrace  trace.TraceID
	wantSpans  int
	flags      trace.TraceFlags
	noRequest  bool
}

func checkRequestSpan(t *testing.T, tt *requestSpanCase) {
	t.Helper()

	traceID := trace.TraceID{1, 2, 3}
	requestSpan := trace.SpanID{4, 5, 6}
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.NeverSample())),
		sdktrace.WithSyncer(exporter),
	)
	previous := otel.GetTracerProvider()

	otel.SetTracerProvider(provider)
	t.Cleanup(func() { otel.SetTracerProvider(previous) })

	rig := newCtxRig(t, true)
	rig.store.open()

	ctx := t.Context()
	if !tt.noRequest {
		ctx = trace.ContextWithRemoteSpanContext(ctx, trace.NewSpanContext(trace.SpanContextConfig{
			TraceID: traceID, SpanID: requestSpan, TraceFlags: tt.flags, Remote: true,
		}))
	}

	res, err := rig.eval.BoolVariationDetails(
		ctx, rig.set, flagAudience, ffcontext.NewEvaluationContext(memberAlice), false)
	require.NoError(t, err, tt.name)
	assert.True(t, res.Value, tt.name)

	spans := exporter.GetSpans()
	require.Len(t, spans, tt.wantSpans, tt.name)

	for i := range spans {
		assert.Equal(t, "segmentLookups", spans[i].Name, tt.name)
		assert.Equal(t, tt.wantParent, spans[i].Parent.SpanID(), tt.name)
		assert.Equal(t, tt.wantTrace, spans[i].SpanContext.TraceID(), tt.name)
	}
}

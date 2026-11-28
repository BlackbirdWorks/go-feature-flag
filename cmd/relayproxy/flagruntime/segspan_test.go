package flagruntime_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

// The lookups of an evaluation are one span when the relay exports traces: the connections, the number of lookups, the
// number unanswered and the result, and never a segment, an identifier or the text of an error.
func TestDynamicSegmentsLookupSpan(t *testing.T) {
	// the tracer provider is global: this test cannot run in parallel with the others that set it
	t.Setenv("SEMAPHORE_TEST_SERIAL", "tracer-provider")

	tests := []spanCase{
		{name: "answered", traced: true, wantSpans: 1, wantResult: "answered", wantStatus: codes.Unset},
		{
			name: "store down", traced: true, fail: errStoreDown, wantSpans: 1, wantResult: "failed",
			wantUnanswered: 1, wantStatus: codes.Error,
		},
		{name: "tracing off", traced: false, wantSpans: 0},
	}

	for i := range tests {
		checkSpan(t, &tests[i])
	}
}

type spanCase struct {
	fail           error
	name           string
	wantResult     string
	wantUnanswered int64
	wantSpans      int
	wantStatus     codes.Code
	traced         bool
}

func checkSpan(t *testing.T, tt *spanCase) {
	t.Helper()

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()

	otel.SetTracerProvider(provider)
	t.Cleanup(func() { otel.SetTracerProvider(previous) })

	source := segment.NewMemorySource(map[string][]string{audienceSegmentName: {memberAlice}})
	source.Fail(segment.OpContains, tt.fail)

	builder := viewBuilderWith(t, &flagruntime.SegmentEnv{Binder: &memBinder{source: source}, Traced: tt.traced})
	built, err := builder.Build(t.Context(), viewContent(t, lookupDoc))
	require.NoError(t, err, tt.name)

	builder.Commit(built)

	eval := flagruntime.NewEvaluator(newService(t), nil)
	_, err = eval.BoolVariationDetails(
		built.Runtime.FlagSets["a"], "audience-flag", ffcontext.NewEvaluationContext(memberAlice), false)
	require.NoError(t, err, tt.name)

	spans := recorder.Ended()
	require.Len(t, spans, tt.wantSpans, tt.name)

	if tt.wantSpans == 0 {
		return
	}

	assert.Equal(t, "segmentLookups", spans[0].Name(), tt.name)
	assert.Equal(t, tt.wantStatus, spans[0].Status().Code, tt.name)

	got := map[string]attribute.Value{}
	for _, kv := range spans[0].Attributes() {
		got[string(kv.Key)] = kv.Value
	}

	assert.Len(t, got, 4, tt.name)
	assert.Equal(t, []string{connAudiences}, got["semaphore.segment.connections"].AsStringSlice(), tt.name)
	assert.Equal(t, int64(1), got["semaphore.segment.lookups"].AsInt64(), tt.name)
	assert.Equal(t, tt.wantUnanswered, got["semaphore.segment.unanswered"].AsInt64(), tt.name)
	assert.Equal(t, tt.wantResult, got["semaphore.segment.result"].AsString(), tt.name)

	for _, kv := range spans[0].Attributes() {
		assert.NotContains(t, kv.Value.String(), memberAlice, "no identifier in a span")
	}
}

// A segment whose breaker opened after failed calls is counted for its connection; the others are not.
func TestSegmentBindingsCountOpenBreakers(t *testing.T) {
	t.Parallel()

	failures := 1
	source := segment.NewMemorySource(map[string][]string{audienceSegmentName: {memberAlice}})
	source.Fail(segment.OpContains, errStoreDown)

	builder := viewBuilderWith(t, &flagruntime.SegmentEnv{
		Binder:   &memBinder{source: source},
		Resolver: &bootstrap.SegmentResolver{Default: bootstrap.SegmentTuning{BreakerFailures: &failures}},
	})
	built, err := builder.Build(t.Context(), viewContent(t, lookupDoc))
	require.NoError(t, err)

	builder.Commit(built)

	assert.Equal(
		t,
		[]flagruntime.ConnectionBreakers{{Connection: connAudiences}},
		built.Runtime.SegmentBindings().Breakers,
	)

	eval := flagruntime.NewEvaluator(newService(t), nil)
	_, err = eval.BoolVariationDetails(
		built.Runtime.FlagSets["a"], "audience-flag", ffcontext.NewEvaluationContext(memberAlice), false)
	require.NoError(t, err)

	assert.Equal(
		t,
		[]flagruntime.ConnectionBreakers{
			{Connection: connAudiences, Open: 1},
		},
		built.Runtime.SegmentBindings().Breakers,
	)
}

// The runtime names the dynamic segments that it serves, sorted and distinct; a flag set with none names nothing.
func TestRuntimeDynamicSegmentNames(t *testing.T) {
	t.Parallel()

	source := segment.NewMemorySource(map[string][]string{audienceSegmentName: {memberAlice}})
	builder := viewBuilder(t, &memBinder{source: source})
	built, err := builder.Build(t.Context(), viewContent(t, lookupDoc))
	require.NoError(t, err)

	builder.Commit(built)

	assert.Equal(t, []string{audienceSegmentName}, built.Runtime.DynamicSegmentNames())
	assert.Empty(t, flagruntime.NewHolder().Load().DynamicSegmentNames())
}

// staffName is a segment that the flags of several fixtures use and no document of them defines.
const staffName = "staff"

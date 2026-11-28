package ofrep_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/handler/ofrep"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/helper"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/testdata/mock"
	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/internal/flagstate"
	"github.com/blackbirdworks/semaphore/internal/protocol"
	coremodel "github.com/blackbirdworks/semaphore/modules/core/model"
)

// reasonStatic is the reason of the evaluations of the stub.
const reasonStatic = "STATIC"

// spanStubFlagSet keeps the span that the context of an evaluation carries.
type spanStubFlagSet struct {
	service.FlagSet

	spans chan trace.SpanContext
}

func (s *spanStubFlagSet) RawVariation(
	ctx context.Context, _ string, _ ffcontext.Context, _ any,
) (coremodel.RawVarResult, error) {
	s.spans <- trace.SpanContextFromContext(ctx)

	return coremodel.RawVarResult{Value: true, VariationType: "v1", Reason: reasonStatic}, nil
}

func (s *spanStubFlagSet) AllFlagsState(ctx context.Context, _ ffcontext.Context) flagstate.AllFlags {
	s.spans <- trace.SpanContextFromContext(ctx)

	return flagstate.NewAllFlags()
}

// The evaluation gets the context of the span of the handler, so that the lookups of the segments are its children.
func TestEvaluationContextCarriesTheSpanOfTheHandler(t *testing.T) {
	// the tracer provider is global: this test cannot run in parallel with the others that set it
	t.Setenv("SEMAPHORE_TEST_SERIAL", "tracer-provider")

	tests := []struct {
		name     string
		path     string
		wantSpan string
	}{
		{name: "OFREP single", path: ofrepEvaluatePrefix + "test-flag", wantSpan: "flagEvaluation"},
		{name: "OFREP bulk", path: "/ofrep/v1/evaluate/flags", wantSpan: "AllFlagsState"},
	}

	for i := range tests {
		tt := &tests[i]

		exporter := tracetest.NewInMemoryExporter()
		provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
		previous := otel.GetTracerProvider()

		otel.SetTracerProvider(provider)
		t.Cleanup(func() { otel.SetTracerProvider(previous) })

		stub := &spanStubFlagSet{spans: make(chan trace.SpanContext, 1)}
		resolver := &mock.Resolver{Set: stub, Name: "default", Default: true}
		ctrl := ofrep.NewOFREPEvaluate(resolver, &metric.Metrics{}, ofrep.EventStream{})
		mux := http.NewServeMux()
		mux.Handle(postOfrepVOneEvaluate, helper.Handle(ctrl.Evaluate, false))
		mux.Handle("POST /ofrep/v1/evaluate/flags", helper.Handle(ctrl.BulkEvaluate, false))

		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, tt.path,
			strings.NewReader(`{"context":{"targetingKey":"user-1"}}`))
		req.Header.Set(protocol.HeaderContentType, protocol.MIMEApplicationJSON)
		mux.ServeHTTP(httptest.NewRecorder(), req)

		got := <-stub.spans
		spans := exporter.GetSpans()
		require.Len(t, spans, 1, tt.name)
		assert.Equal(t, tt.wantSpan, spans[0].Name, tt.name)
		assert.Equal(t, spans[0].SpanContext.SpanID(), got.SpanID(), tt.name)
		assert.Equal(t, spans[0].SpanContext.TraceID(), got.TraceID(), tt.name)
	}
}

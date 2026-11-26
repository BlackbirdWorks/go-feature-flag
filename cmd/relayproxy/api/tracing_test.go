package api_test

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/apiwire"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/model"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/testdata/mock"
	"github.com/blackbirdworks/semaphore/internal/protocol"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
	"github.com/blackbirdworks/semaphore/testhelpers/slogutil"
)

const (
	serverError = "Server error"
)

var errNotInitialized = errors.New("not initialized")

const traceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0e0e0e0e-01"

// failingMonitoring is a monitoring service whose info endpoint fails.
func failingMonitoring() *mock.MockMonitoringService {
	return &mock.MockMonitoringService{
		HealthResponse: model.HealthResponse{Initialized: true},
		InfoError:      errNotInitialized,
	}
}

func Test_Tracing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		wantName   string
		wantRoute  string
		wantRecord string
		wantStatus int
		wantError  bool
	}{
		{
			name:       "huma operation",
			method:     http.MethodGet,
			path:       healthPath,
			wantName:   getHealthRoute,
			wantRoute:  healthPath,
			wantRecord: fixtures.LogMessageSuccess,
			wantStatus: http.StatusOK,
		},
		{
			name:       "huma operation failing",
			method:     http.MethodGet,
			path:       info,
			wantName:   "GET /info",
			wantRoute:  info,
			wantRecord: serverError,
			wantStatus: http.StatusInternalServerError,
			wantError:  true,
		},
		{
			name:       "raw handler",
			method:     http.MethodPost,
			path:       "/v1/feature/some-flag/eval",
			body:       `{"evaluationContext":{"key":"u"}}`,
			wantName:   "POST /v1/feature/{flagKey}/eval",
			wantRoute:  "/v1/feature/{flagKey}/eval",
			wantRecord: serverError,
			wantStatus: http.StatusInternalServerError,
			wantError:  true,
		},
		{
			name:       "no route",
			method:     http.MethodGet,
			path:       nope,
			wantName:   "GET /",
			wantRoute:  "/",
			wantRecord: "Client error",
			wantStatus: http.StatusNotFound,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			recorder := tracetest.NewSpanRecorder()
			provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))

			t.Cleanup(func() { _ = provider.Shutdown(t.Context()) })

			conf := testConfig(t)
			// /health is not logged at the default level: the debug level logs everything.
			conf.LogLevel = "debug"
			logger, logs := slogutil.NewObserver(slog.LevelDebug)
			// without a flag set manager the endpoints that need one answer 500: the span of a failure is checked.
			srv := apiwire.New(conf, &service.Services{MonitoringService: failingMonitoring()}, logger,
				api.WithTracing(provider, propagation.TraceContext{}))

			req := httptest.NewRequestWithContext(t.Context(), tt.method, tt.path, strings.NewReader(tt.body))
			req.Header.Set(protocol.HeaderContentType, protocol.MIMEApplicationJSON)
			req.Header.Set("Traceparent", traceparent)

			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			assert.Equal(t, tt.wantStatus, rec.Code)

			spans := recorder.Ended()
			require.Len(t, spans, 1)
			span := spans[0]
			assert.Equal(t, tt.wantName, span.Name())
			assert.Equal(t, trace.SpanKindServer, span.SpanKind())
			assert.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", span.SpanContext().TraceID().String())
			assert.Equal(
				t,
				"00f067aa0e0e0e0e",
				span.Parent().SpanID().String(),
				"the incoming traceparent is the parent",
			)

			// the access record carries the trace and span IDs of the span, like the other lines of the request.
			entries := logs.All()

			records := make([]string, 0, len(entries))
			for _, entry := range entries {
				fields := entry.ContextMap()
				assert.Equal(t, span.SpanContext().TraceID().String(), fields["trace_id"], entry.Message)
				assert.Equal(t, span.SpanContext().SpanID().String(), fields["span_id"], entry.Message)
				records = append(records, entry.Message)
			}

			assert.Contains(t, records, tt.wantRecord)

			attrs := map[attribute.Key]attribute.Value{}
			for _, kv := range span.Attributes() {
				attrs[kv.Key] = kv.Value
			}

			assert.Equal(t, int64(tt.wantStatus), attrs["http.response.status_code"].AsInt64())
			assert.Equal(t, tt.method, attrs["http.request.method"].AsString())
			assert.Equal(t, tt.wantRoute, attrs["http.route"].AsString())

			if tt.wantError {
				assert.Equal(t, codes.Error, span.Status().Code)
			} else {
				assert.Equal(t, codes.Unset, span.Status().Code)
			}
		})
	}
}

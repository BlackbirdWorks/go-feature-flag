package middleware_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/blackbirdworks/huma-middleware/middleware/accesslog"
	"github.com/blackbirdworks/huma-middleware/middleware/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"

	"github.com/blackbirdworks/semaphore/cmdhelpers/api/middleware"
	"github.com/blackbirdworks/semaphore/testutils/slogutil"
)

// requestLogger is the access log of a plain handler, as the relay proxy builds it.
func requestLogger(logger *slog.Logger, debug bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		access := accesslog.Handler(
			accesslog.WithLogger(logger),
			accesslog.WithRecordFunc(middleware.AccessLog()),
			accesslog.WithRequestSkip(middleware.SkipRequest(debug)),
		)

		return logging.Handler(access(next), logging.WithLogger(logger))
	}
}

func TestRequestLogger200(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/something", nil)
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	obs, logs := slogutil.NewObserver(slog.LevelDebug)
	logger := obs
	requestLogger(logger, false)(h).ServeHTTP(httptest.NewRecorder(), req)
	logFields := logs.All()[0].ContextMap()
	assert.Equal(t, 1, logs.Len())
	assert.Equal(t, int64(200), logFields["status"])
	assert.NotNil(t, logFields["latency"])
	assert.Equal(t, "GET /something", logFields["request"])
	assert.NotNil(t, logFields["host"])
	assert.NotNil(t, logFields["size"])
}

func TestRequestLogger300(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/something", nil)
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusFound) })

	obs, logs := slogutil.NewObserver(slog.LevelDebug)
	logger := obs
	requestLogger(logger, false)(h).ServeHTTP(httptest.NewRecorder(), req)
	logFields := logs.All()[0].ContextMap()
	assert.Equal(t, 1, logs.Len())
	assert.Equal(t, int64(302), logFields["status"])
	assert.NotNil(t, logFields["latency"])
	assert.Equal(t, "GET /something", logFields["request"])
	assert.NotNil(t, logFields["host"])
	assert.NotNil(t, logFields["size"])
}
func TestRequestLogger400(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/something", nil)
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadRequest) })

	obs, logs := slogutil.NewObserver(slog.LevelDebug)
	logger := obs
	requestLogger(logger, false)(h).ServeHTTP(httptest.NewRecorder(), req)
	logFields := logs.All()[0].ContextMap()
	assert.Equal(t, 1, logs.Len())
	assert.Equal(t, int64(400), logFields["status"])
	assert.NotNil(t, logFields["latency"])
	assert.Equal(t, "GET /something", logFields["request"])
	assert.NotNil(t, logFields["host"])
	assert.NotNil(t, logFields["size"])
}

func TestRequestLogger500(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/something", nil)
	handler := http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) },
	)

	obs, logs := slogutil.NewObserver(slog.LevelError)
	logger := obs
	requestLogger(logger, false)(handler).ServeHTTP(httptest.NewRecorder(), req)
	logFields := logs.All()[0].ContextMap()
	assert.Equal(t, 1, logs.Len())
	assert.Equal(t, int64(500), logFields["status"])
	assert.NotNil(t, logFields["latency"])
	assert.Equal(t, "GET /something", logFields["request"])
	assert.NotNil(t, logFields["host"])
	assert.NotNil(t, logFields["size"])
}

func TestRequestLoggerHealth(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	handler := http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) },
	)

	obs, logs := slogutil.NewObserver(slog.LevelDebug)
	logger := obs
	requestLogger(logger, false)(handler).ServeHTTP(httptest.NewRecorder(), req)
	assert.Empty(t, logs.All())
}

func TestRequestLoggerHealthDebug(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	handler := http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) },
	)

	obs, logs := slogutil.NewObserver(slog.LevelDebug)
	logger := obs
	requestLogger(logger, true)(handler).ServeHTTP(httptest.NewRecorder(), req)
	assert.Len(t, logs.All(), 1)
}

func TestRequestLoggerRedactsAPIKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		uri  string
		want string
	}{
		{
			name: "no query",
			uri:  "/ws/v1/flag/change",
			want: "GET /ws/v1/flag/change",
		},
		{
			name: "apiKey only",
			uri:  "/stream/v1/sse/flag/change?apiKey=secret",
			want: "GET /stream/v1/sse/flag/change?apiKey=REDACTED",
		},
		{
			name: "apiKey among other params",
			uri:  "/stream/v1/ws/flag/change?a=1&apiKey=secret&b=2",
			want: "GET /stream/v1/ws/flag/change?a=1&apiKey=REDACTED&b=2",
		},
		{
			name: "no apiKey",
			uri:  "/stream/v1/ws/flag/change?a=1&b=2",
			want: "GET /stream/v1/ws/flag/change?a=1&b=2",
		},
		{
			name: "percent-encoded apiKey name",
			uri:  "/ws/v1/flag/change?api%4Bey=secret",
			want: "GET /ws/v1/flag/change?api%4Bey=REDACTED",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodGet, tt.uri, nil)
			h := http.HandlerFunc(
				func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) },
			)

			obs, logs := slogutil.NewObserver(slog.LevelDebug)
			requestLogger(obs, false)(h).ServeHTTP(httptest.NewRecorder(), req)
			assert.Equal(t, 1, logs.Len())
			assert.Equal(t, tt.want, logs.All()[0].ContextMap()["request"])
		})
	}
}

var errBoom = errors.New("boom")

func TestLogRequest(t *testing.T) {
	t.Parallel()

	tracedSpan := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{
			0x4b, 0xf9, 0x2f, 0x35, 0x77, 0xb3, 0x4d, 0xa6, 0xa3, 0xce, 0x92, 0x9d, 0x0e, 0x0e, 0x47, 0x36,
		},
		SpanID: trace.SpanID{0x00, 0xf0, 0x67, 0xaa, 0x0e, 0x0e, 0x0e, 0x0e},
	})

	tests := []struct {
		wantContext map[string]any
		name        string
		wantMessage string
		span        trace.SpanContext
		rec         middleware.RequestRecord
		wantLevel   slog.Level
	}{
		{
			name: "server error",
			rec: middleware.RequestRecord{
				Status: http.StatusBadGateway, Method: http.MethodPost, URI: "/v1/eval", Err: errBoom,
				RemoteIP: "10.0.0.1", Host: "relay", UserAgent: "curl", RequestID: "id-1", Size: 7,
				Latency: 1500 * time.Microsecond,
			},
			wantLevel: slog.LevelError, wantMessage: "Server error",
			wantContext: map[string]any{
				"error": "boom", "remote_ip": "10.0.0.1", "latency": "1.5ms", "host": "relay",
				"request": "POST /v1/eval", "status": int64(502), "size": int64(7), "user_agent": "curl",
				"request_id": "id-1",
			},
		},
		{
			name: "client error redacts the apiKey",
			rec: middleware.RequestRecord{
				Status: http.StatusUnauthorized, Method: http.MethodGet, URI: "/ws/v1/flag/change?apiKey=s3cr3t&a=b",
			},
			wantLevel: slog.LevelWarn, wantMessage: "Client error",
			wantContext: map[string]any{
				"remote_ip": "", "latency": "0s", "host": "", "request": "GET /ws/v1/flag/change?apiKey=REDACTED&a=b",
				"status": int64(401), "size": int64(0), "user_agent": "", "request_id": "",
			},
		},
		{
			name:      "traced",
			rec:       middleware.RequestRecord{Status: http.StatusOK, Method: http.MethodGet, URI: "/"},
			span:      tracedSpan,
			wantLevel: slog.LevelDebug, wantMessage: "Success",
			wantContext: map[string]any{
				"remote_ip": "", "latency": "0s", "host": "", "request": "GET /", "status": int64(200),
				"size": int64(0), "user_agent": "", "request_id": "", "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736",
				"span_id": "00f067aa0e0e0e0e",
			},
		},
		{
			name:      "redirection",
			rec:       middleware.RequestRecord{Status: http.StatusFound, Method: http.MethodGet, URI: "/"},
			wantLevel: slog.LevelDebug, wantMessage: "Redirection",
			wantContext: map[string]any{
				"remote_ip": "", "latency": "0s", "host": "", "request": "GET /", "status": int64(302),
				"size": int64(0), "user_agent": "", "request_id": "",
			},
		},
		{
			name:      "success",
			rec:       middleware.RequestRecord{Status: http.StatusOK, Method: http.MethodGet, URI: "/?x"},
			wantLevel: slog.LevelDebug, wantMessage: "Success",
			wantContext: map[string]any{
				"remote_ip": "", "latency": "0s", "host": "", "request": "GET /?x", "status": int64(200),
				"size": int64(0), "user_agent": "", "request_id": "",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			obs, logs := slogutil.NewObserver(slog.LevelDebug)
			ctx := trace.ContextWithSpanContext(t.Context(), tt.span)
			middleware.LogRequest(ctx, obs, &tt.rec)

			got := logs.All()
			require.Len(t, got, 1)
			assert.Equal(t, tt.wantLevel, got[0].Level)
			assert.Equal(t, tt.wantMessage, got[0].Message)
			assert.Equal(t, tt.wantContext, got[0].ContextMap())
		})
	}
}

// TestLogRequestDisabledLevel checks that a disabled level costs nothing: no record, no allocation.
// It is not parallel on purpose: AllocsPerRun counts the allocations of the parallel tests too.
//
//nolint:paralleltest // see above
func TestLogRequestDisabledLevel(t *testing.T) {
	obs, logs := slogutil.NewObserver(slog.LevelError)
	rec := middleware.RequestRecord{Status: http.StatusOK, Method: http.MethodGet, URI: "/v1/eval?apiKey=x"}
	ctx := context.Background()

	allocs := testing.AllocsPerRun(100, func() { middleware.LogRequest(ctx, obs, &rec) })

	assert.InDelta(t, 0, allocs, 0)
	assert.Zero(t, logs.Len())
}

func TestRequestLoggerRequestID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		requestHeader  string
		responseHeader string
		wantRequestID  string
	}{
		{name: "from the request", requestHeader: "from-request", wantRequestID: "from-request"},
		{name: "from the response", responseHeader: "from-response", wantRequestID: "from-response"},
		{name: "the request wins", requestHeader: "from-request", responseHeader: "from-response",
			wantRequestID: "from-request"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodGet, "/something", nil)
			if tt.requestHeader != "" {
				req.Header.Set("X-Request-ID", tt.requestHeader)
			}
			obs, logs := slogutil.NewObserver(slog.LevelDebug)
			handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tt.responseHeader != "" {
					w.Header().Set("X-Request-ID", tt.responseHeader)
				}
				w.WriteHeader(http.StatusOK)
			})

			requestLogger(obs, false)(handler).ServeHTTP(httptest.NewRecorder(), req)

			require.Equal(t, 1, logs.Len())
			assert.Equal(t, tt.wantRequestID, logs.All()[0].ContextMap()["request_id"])
		})
	}
}

// TestRequestLoggerReturnedErrors drives requests through the logger to handlers that fail or panic. The record
// carries the status that was sent to the client, at the level of its class (issue 63): a handler that returned
// an error is logged with its error status, and a recovered panic is a 500 whose error is in the record.
func TestRequestLoggerReturnedErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		handler     func(http.ResponseWriter, *http.Request)
		name        string
		wantMessage string
		wantLevel   slog.Level
		wantStatus  int
		wantErr     bool
	}{
		{
			name:        "not found",
			handler:     func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) },
			wantStatus:  http.StatusNotFound,
			wantLevel:   slog.LevelWarn,
			wantMessage: "Client error",
		},
		{name: "unauthorized", handler: func(w http.ResponseWriter, r *http.Request) {
			middleware.RecordError(r.Context(), errBoom)
			w.WriteHeader(http.StatusUnauthorized)
		}, wantStatus: http.StatusUnauthorized, wantLevel: slog.LevelWarn, wantMessage: "Client error", wantErr: true},
		{name: "server error", handler: func(w http.ResponseWriter, r *http.Request) {
			middleware.RecordError(r.Context(), errBoom)
			w.WriteHeader(http.StatusInternalServerError)
		},
			wantStatus: http.StatusInternalServerError, wantLevel: slog.LevelError,
			wantMessage: "Server error", wantErr: true},
		{
			name:        "no status is a 200",
			handler:     func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("x")) },
			wantStatus:  http.StatusOK,
			wantLevel:   slog.LevelDebug,
			wantMessage: "Success",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			obs, logs := slogutil.NewObserver(slog.LevelDebug)
			h := requestLogger(obs, false)(http.HandlerFunc(tt.handler))
			rec := httptest.NewRecorder()

			h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x", nil))

			assert.Equal(t, tt.wantStatus, rec.Code)
			got := logs.All()
			require.Len(t, got, 1)
			assert.Equal(t, tt.wantLevel, got[0].Level)
			assert.Equal(t, tt.wantMessage, got[0].Message)
			assert.Equal(t, int64(tt.wantStatus), got[0].ContextMap()["status"])
			if tt.wantErr {
				assert.Equal(t, errBoom.Error(), got[0].ContextMap()["error"])
			}
		})
	}
}

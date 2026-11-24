package middleware_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
	echomiddleware "github.com/labstack/echo/v5/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmdhelpers/api/middleware"
	"github.com/blackbirdworks/semaphore/testutils/slogutil"
)

func TestRequestLogger200(t *testing.T) {
	t.Parallel()

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/something", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	h := func(c *echo.Context) error {
		return c.String(http.StatusOK, "")
	}

	obs, logs := slogutil.NewObserver(slog.LevelDebug)
	logger := obs
	err := middleware.RequestLogger(logger, false)(h)(c)
	require.NoError(t, err)
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

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/something", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	h := func(c *echo.Context) error {
		return c.String(http.StatusFound, "")
	}

	obs, logs := slogutil.NewObserver(slog.LevelDebug)
	logger := obs
	err := middleware.RequestLogger(logger, false)(h)(c)
	require.NoError(t, err)
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

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/something", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	h := func(c *echo.Context) error {
		return c.String(http.StatusBadRequest, "")
	}

	obs, logs := slogutil.NewObserver(slog.LevelDebug)
	logger := obs
	err := middleware.RequestLogger(logger, false)(h)(c)
	require.NoError(t, err)
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

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/something", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	h := func(c *echo.Context) error {
		return c.String(http.StatusInternalServerError, "")
	}

	obs, logs := slogutil.NewObserver(slog.LevelError)
	logger := obs
	err := middleware.RequestLogger(logger, false)(h)(c)
	require.NoError(t, err)
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

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	h := func(c *echo.Context) error {
		return c.String(http.StatusInternalServerError, "")
	}

	obs, logs := slogutil.NewObserver(slog.LevelDebug)
	logger := obs
	err := middleware.RequestLogger(logger, false)(h)(c)
	require.NoError(t, err)
	assert.Empty(t, logs.All())
}

func TestRequestLoggerHealthDebug(t *testing.T) {
	t.Parallel()

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	h := func(c *echo.Context) error {
		return c.String(http.StatusInternalServerError, "")
	}

	obs, logs := slogutil.NewObserver(slog.LevelDebug)
	logger := obs
	err := middleware.RequestLogger(logger, true)(h)(c)
	require.NoError(t, err)
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

			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, tt.uri, nil)
			c := e.NewContext(req, httptest.NewRecorder())
			h := func(c *echo.Context) error {
				return c.String(http.StatusUnauthorized, "")
			}

			obs, logs := slogutil.NewObserver(slog.LevelDebug)
			err := middleware.RequestLogger(obs, false)(h)(c)
			require.NoError(t, err)
			assert.Equal(t, 1, logs.Len())
			assert.Equal(t, tt.want, logs.All()[0].ContextMap()["request"])
		})
	}
}

var errBoom = errors.New("boom")

func TestLogRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantContext map[string]any
		name        string
		wantMessage string
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
			middleware.LogRequest(t.Context(), obs, &tt.rec)

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

func TestRequestLoggerRequestIDFromResponse(t *testing.T) {
	t.Parallel()

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/something", nil)
	c := e.NewContext(req, httptest.NewRecorder())
	obs, logs := slogutil.NewObserver(slog.LevelDebug)
	h := func(c *echo.Context) error {
		c.Response().Header().Set(echo.HeaderXRequestID, "from-response")

		return c.String(http.StatusOK, "")
	}

	require.NoError(t, middleware.RequestLogger(obs, false)(h)(c))

	assert.Equal(t, "from-response", logs.All()[0].ContextMap()["request_id"])
}

// TestRequestLoggerReturnedErrors drives real requests through the echo stack to handlers that return an error or
// panic. The logger reads the status of the response when the handler returns, before echo's error handler writes
// the status of a returned error: the record is the "Success" one with status 200, at the debug level, exactly as
// the zap based middleware of the previous version logged it (checked on main). The test pins that behaviour.
func TestRequestLoggerReturnedErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		handler        func(*echo.Context) error
		name           string
		wantRespStatus int
	}{
		{name: "not found", handler: func(*echo.Context) error { return echo.NewHTTPError(http.StatusNotFound, "nf") },
			wantRespStatus: http.StatusNotFound},
		{
			name:           "unauthorized",
			handler:        func(*echo.Context) error { return echo.NewHTTPError(http.StatusUnauthorized, "no") },
			wantRespStatus: http.StatusUnauthorized,
		},
		{name: "plain error", handler: func(*echo.Context) error { return errBoom },
			wantRespStatus: http.StatusInternalServerError},
		{name: "panic", handler: func(*echo.Context) error { panic("boom") },
			wantRespStatus: http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			obs, logs := slogutil.NewObserver(slog.LevelDebug)
			e := echo.New()
			e.Use(middleware.RequestLogger(obs, false))
			e.Use(echomiddleware.Recover())
			e.GET("/x", tt.handler)
			rec := httptest.NewRecorder()

			e.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x", nil))

			assert.Equal(t, tt.wantRespStatus, rec.Code)
			got := logs.All()
			require.Len(t, got, 1)
			assert.Equal(t, slog.LevelDebug, got[0].Level)
			assert.Equal(t, "Success", got[0].Message)
			assert.Equal(t, int64(http.StatusOK), got[0].ContextMap()["status"])
		})
	}
}

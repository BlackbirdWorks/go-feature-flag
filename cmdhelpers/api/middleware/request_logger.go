package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"

	cmdlog "github.com/blackbirdworks/semaphore/cmdhelpers/log"
)

// DefaultSkipper is what we use as a default.
// Some endpoints are excluded from the logs to avoid flooding the logs and
// because they are not bringing a lot of value.
func DefaultSkipper(c *echo.Context) bool {
	skipperURL := []string{"/health", "/info", "/metrics"}
	for _, ignoredPath := range skipperURL {
		if strings.HasPrefix(ignoredPath, c.Request().URL.String()) {
			return true
		}
	}

	return false
}

// DebugSkipper is the skipper used in debug mode, we log everything.
func DebugSkipper(_ *echo.Context) bool {
	return false
}

// RequestRecord is what the request logger logs about a request. It holds no framework type, so the logging is
// the same whatever the router that fills it.
type RequestRecord struct {
	Err       error
	RemoteIP  string
	Host      string
	Method    string
	URI       string
	UserAgent string
	RequestID string
	Latency   time.Duration
	Size      int64
	Status    int
}

// LogRequest logs a request at the level of its status: server errors are errors, client errors warnings,
// the rest is debug. Nothing is built when the level is disabled.
func LogRequest(ctx context.Context, logger *slog.Logger, r *RequestRecord) {
	var (
		level slog.Level
		msg   string
	)
	switch n := r.Status; {
	case n >= http.StatusInternalServerError:
		level, msg = slog.LevelError, "Server error"
	case n >= http.StatusBadRequest:
		level, msg = slog.LevelWarn, "Client error"
	case n >= http.StatusMultipleChoices:
		level, msg = slog.LevelDebug, "Redirection"
	default:
		level, msg = slog.LevelDebug, "Success"
	}
	if !logger.Enabled(ctx, level) {
		return
	}

	logger.LogAttrs(ctx, level, msg,
		cmdlog.Err(r.Err),
		slog.String("remote_ip", r.RemoteIP),
		slog.String("latency", r.Latency.String()),
		slog.String("host", r.Host),
		slog.String("request", r.Method+" "+redactAPIKey(r.URI)),
		slog.Int("status", r.Status),
		slog.Int64("size", r.Size),
		slog.String("user_agent", r.UserAgent),
		slog.String("request_id", r.RequestID),
	)
}

// RequestLogger is a middleware to provide an "access log" like logging for each request.
func RequestLogger(logger *slog.Logger, isDebugEnabled bool) echo.MiddlewareFunc {
	// select the right skipper
	skipper := DefaultSkipper
	if isDebugEnabled {
		skipper = DebugSkipper
	}

	return middleware.RequestLoggerWithConfig(middleware.RequestLoggerConfig{
		Skipper: skipper,
		LogValuesFunc: func(c *echo.Context, v middleware.RequestLoggerValues) error {
			req := c.Request()
			res := c.Response()
			rec := RequestRecord{
				Err:       v.Error,
				RemoteIP:  c.RealIP(),
				Latency:   time.Since(v.StartTime),
				Host:      req.Host,
				Method:    req.Method,
				URI:       req.RequestURI,
				UserAgent: req.UserAgent(),
				RequestID: req.Header.Get(echo.HeaderXRequestID),
			}
			if r, err := echo.UnwrapResponse(res); err == nil {
				rec.Status = r.Status
				rec.Size = r.Size
			}
			if rec.RequestID == "" {
				rec.RequestID = res.Header().Get(echo.HeaderXRequestID)
			}
			LogRequest(req.Context(), logger, &rec)

			return nil
		},
	})
}

// redactAPIKey masks the apiKey query parameter value so it never reaches the logs.
// Stream endpoints receive the key as a query param, because browsers can't set websocket headers (#4330).
func redactAPIKey(uri string) string {
	path, query, found := strings.Cut(uri, "?")
	if !found {
		return uri
	}
	params := strings.Split(query, "&")
	for i, p := range params {
		key, _, _ := strings.Cut(p, "=")
		if k, err := url.QueryUnescape(key); err == nil && k == "apiKey" {
			params[i] = key + "=REDACTED"
		}
	}

	return path + "?" + strings.Join(params, "&")
}

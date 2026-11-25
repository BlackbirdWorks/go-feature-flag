package middleware

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/blackbirdworks/huma-middleware/middleware/accesslog"
	"github.com/blackbirdworks/huma-middleware/middleware/logging"
	"github.com/danielgtaylor/huma/v2"
	"go.opentelemetry.io/otel/trace"

	cmdlog "github.com/blackbirdworks/semaphore/cmdhelpers/log"
)

// skipURL is what we use as a default.
// Some endpoints are excluded from the logs to avoid flooding the logs and
// because they are not bringing a lot of value.
func skipURL(u *url.URL) bool {
	skipperURL := []string{"/health", "/info", "/metrics"}
	for _, ignoredPath := range skipperURL {
		if strings.HasPrefix(ignoredPath, u.String()) {
			return true
		}
	}

	return false
}

// SkipHuma is the skipper of the access log for a huma operation: the monitoring endpoints are not logged, unless
// the debug level is on and everything is logged.
func SkipHuma(debug bool) func(huma.Context) bool {
	return func(ctx huma.Context) bool {
		u := ctx.URL()

		return !debug && skipURL(&u)
	}
}

// SkipRequest is [SkipHuma] for a plain [http.Handler].
func SkipRequest(debug bool) func(*http.Request) bool {
	return func(r *http.Request) bool { return !debug && skipURL(r.URL) }
}

// RecordError keeps err as the error of the request: the access log reports it.
func RecordError(ctx context.Context, err error) {
	if err != nil {
		logging.AddAttrs(ctx, slog.Any(errorKey, err))
	}
}

// errorKey is the name of the attribute that carries the error of the request.
const errorKey = "error"

// AccessLog writes the access log record of a request, the one [LogRequest] defines, from what the accesslog
// middleware of huma-middleware knows about it. It is the function given to accesslog.WithRecordFunc: the record is
// pooled, everything is copied during the call. The error of the request ([RecordError]) is not read from the
// record: the logger the function receives is the one of the request logger, which adds the attributes of the request
// (the error among them) to the line.
func AccessLog() func(ctx context.Context, l *slog.Logger, r *accesslog.Record) {
	return func(ctx context.Context, l *slog.Logger, r *accesslog.Record) {
		rec := RequestRecord{
			RemoteIP:  remoteIP(r.RemoteAddr),
			Latency:   r.Duration,
			Host:      r.Host,
			Method:    r.Method,
			URI:       r.RequestURI(),
			UserAgent: r.UserAgent,
			RequestID: r.RequestID,
			Status:    r.Status,
			Size:      r.Size,
		}
		LogRequest(ctx, l, &rec)
	}
}

// remoteIP returns the address of the peer of the connection, without its port.
func remoteIP(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}

	return host
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

	// a record function replaces the record of the library, which adds the IDs of the span: do it here, once the level
	// is known to be enabled (formatting the IDs allocates).
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		logger = logger.With(
			slog.String("trace_id", sc.TraceID().String()),
			slog.String("span_id", sc.SpanID().String()),
		)
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
		k, err := url.QueryUnescape(key)
		if err == nil && k == "apiKey" {
			params[i] = key + "=REDACTED"
		}
	}

	return path + "?" + strings.Join(params, "&")
}

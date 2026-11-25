package api

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/blackbirdworks/huma-middleware/middleware/logging"
)

// bodyDumpSkipper returns the skipper of the body dump middleware.
// The dump is skipped for swagger, when the debug level is disabled, and for the flag file validation
// endpoint to avoid buffering and logging the flag files sent to it.
func bodyDumpSkipper(logger *slog.Logger) func(r *http.Request) bool {
	return func(r *http.Request) bool {
		path := r.URL.Path

		return strings.HasPrefix(path, "/swagger") ||
			path == "/admin/v1/flags/validate" ||
			!logger.Enabled(context.Background(), slog.LevelDebug)
	}
}

// bodyDumpHandler logs request bodies with truncation for large payloads.
// Bodies larger than maxBodyLogSize are truncated and suffixed with a marker.
func bodyDumpHandler() func(r *http.Request, body []byte) {
	maxBodyLogSize := 8192 // 8KiB
	truncatedBodySuffix := []byte("... truncated ...")
	truncatedSize := maxBodyLogSize + len(truncatedBodySuffix)

	// use a sync.Pool to reuse the same buffer for large requests - this
	// reduces allocations and can significantly improve performance.
	bufferPool := &sync.Pool{
		New: func() any {
			buf := make([]byte, truncatedSize)
			copy(buf[maxBodyLogSize:], truncatedBodySuffix)

			return &buf
		},
	}

	return func(r *http.Request, reqBody []byte) {
		ctx := r.Context()
		logger := logging.FromContext(ctx)
		if len(reqBody) <= maxBodyLogSize {
			logger.LogAttrs(
				ctx,
				slog.LevelDebug,
				"Request info",
				slog.String("request_body", string(reqBody)),
			)

			return
		}

		// body is too large, truncate the request body and log it
		bufPtr := bufferPool.Get().(*[]byte) //nolint:errcheck // the pool only ever holds *[]byte (see its New func)
		truncated := *bufPtr
		copy(truncated[:maxBodyLogSize], reqBody[:maxBodyLogSize])
		logger.LogAttrs(
			ctx,
			slog.LevelDebug,
			"Request info",
			slog.String("request_body", string(truncated)),
		)
		bufferPool.Put(bufPtr)
	}
}

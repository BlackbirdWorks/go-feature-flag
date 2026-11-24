package api

import (
	"context"
	"log/slog"
	"strings"
	"sync"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
)

// bodyDumpSkipper returns the skipper of the body dump middleware.
// The dump is skipped for swagger, when the debug level is disabled, and for the flag file validation
// endpoint to avoid buffering and logging the flag files sent to it.
func bodyDumpSkipper(logger *slog.Logger) func(c *echo.Context) bool {
	return func(c *echo.Context) bool {
		path := c.Request().URL.Path

		return strings.HasPrefix(path, "/swagger") ||
			path == "/admin/v1/flags/validate" ||
			!logger.Enabled(context.Background(), slog.LevelDebug)
	}
}

// bodyDumpHandler logs request bodies with truncation for large payloads.
// Bodies larger than maxBodyLogSize are truncated and suffixed with a marker.
func bodyDumpHandler(logger *slog.Logger) middleware.BodyDumpHandler {
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

	return func(_ *echo.Context, reqBody, _ []byte, _ error) {
		if len(reqBody) <= maxBodyLogSize {
			logger.LogAttrs(
				context.Background(),
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
			context.Background(),
			slog.LevelDebug,
			"Request info",
			slog.String("request_body", string(truncated)),
		)
		bufferPool.Put(bufPtr)
	}
}

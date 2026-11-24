package api

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/testutils/slogutil"
)

func Test_bodyDumpSkipper(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		path  string
		level slog.Level
		want  bool
	}{
		{name: "debug on regular endpoint", level: slog.LevelDebug, path: "/v1/allflags", want: false},
		{name: "debug on swagger", level: slog.LevelDebug, path: "/swagger/index.html", want: true},
		{name: "debug on validate endpoint", level: slog.LevelDebug, path: "/admin/v1/flags/validate", want: true},
		{
			name:  "debug on other admin endpoint",
			level: slog.LevelDebug,
			path:  "/admin/v1/retriever/refresh",
			want:  false,
		},
		{name: "info level", level: slog.LevelInfo, path: "/v1/allflags", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			core, _ := slogutil.NewObserver(tt.level)
			skipper := bodyDumpSkipper(core)
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, tt.path, nil)
			c := echo.New().NewContext(req, httptest.NewRecorder())
			assert.Equal(t, tt.want, skipper(c))
		})
	}
}

func Test_bodyDumpHandler_smallBody(t *testing.T) {
	t.Parallel()

	core, recorded := slogutil.NewObserver(slog.LevelDebug)
	logger := core

	body := []byte("small body")
	handler := bodyDumpHandler(logger)
	handler(nil, body, nil, nil)

	logs := recorded.All()
	assert.Len(t, logs, 1)
	assert.Equal(t, "Request info", logs[0].Message)

	assert.Equal(t, string(body), logs[0].ContextMap()["request_body"])
}

func Test_bodyDumpHandler_largeBody(t *testing.T) {
	t.Parallel()

	core, recorded := slogutil.NewObserver(slog.LevelDebug)
	logger := core

	body := []byte(strings.Repeat("x", 20*1024)) // 20KB
	handler := bodyDumpHandler(logger)
	handler(nil, body, nil, nil)

	logs := recorded.All()
	assert.Len(t, logs, 1)

	loggedBody, ok := logs[0].ContextMap()["request_body"].(string)
	require.True(t, ok)
	assert.Less(t, len(loggedBody), len(body), "logged body should be truncated")
	assert.True(t, strings.HasSuffix(loggedBody, "... truncated ..."))
}

func Benchmark_bodyDumpHandler(b *testing.B) {
	core, _ := slogutil.NewObserver(slog.LevelDebug)
	logger := core
	handler := bodyDumpHandler(logger)

	b.Run("small body", func(b *testing.B) {
		body := []byte(strings.Repeat("x", 100))
		b.ReportAllocs()
		b.ResetTimer()

		for b.Loop() {
			handler(nil, body, nil, nil)
		}
	})

	b.Run("large body", func(b *testing.B) {
		body := []byte(strings.Repeat("x", 1024*1024)) // 1MB
		b.ReportAllocs()
		b.ResetTimer()

		for b.Loop() {
			handler(nil, body, nil, nil)
		}
	})
}

package api

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blackbirdworks/huma-middleware/middleware/logging"
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
			assert.Equal(t, tt.want, skipper(req))
		})
	}
}

func Test_bodyDumpHandler(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		body          string
		wantBody      string
		wantTruncated bool
	}{
		{name: "small body", body: "small body", wantBody: "small body"},
		{
			name:          "large body",
			body:          strings.Repeat("x", 20*1024),
			wantBody:      strings.Repeat("x", 8192) + "... truncated ...",
			wantTruncated: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			logger, recorded := slogutil.NewObserver(slog.LevelDebug)
			req := httptest.NewRequestWithContext(logging.NewContext(t.Context(), logger), http.MethodPost, "/", nil)
			bodyDumpHandler()(req, []byte(tt.body))

			logs := recorded.All()
			require.Len(t, logs, 1)
			assert.Equal(t, "Request info", logs[0].Message)
			loggedBody, ok := logs[0].ContextMap()["request_body"].(string)
			require.True(t, ok)
			assert.Equal(t, tt.wantBody, loggedBody)
			assert.Equal(t, tt.wantTruncated, len(loggedBody) < len(tt.body))
		})
	}
}

func Benchmark_bodyDumpHandler(b *testing.B) {
	core, _ := slogutil.NewObserver(slog.LevelDebug)
	logger := core
	handler := bodyDumpHandler()
	req := httptest.NewRequestWithContext(logging.NewContext(b.Context(), logger), http.MethodPost, "/", nil)

	b.Run("small body", func(b *testing.B) {
		body := []byte(strings.Repeat("x", 100))
		b.ReportAllocs()
		b.ResetTimer()

		for b.Loop() {
			handler(req, body)
		}
	})

	b.Run("large body", func(b *testing.B) {
		body := []byte(strings.Repeat("x", 1024*1024)) // 1MB
		b.ReportAllocs()
		b.ResetTimer()

		for b.Loop() {
			handler(req, body)
		}
	})
}

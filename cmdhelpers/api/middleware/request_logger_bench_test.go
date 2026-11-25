package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmdhelpers/cmdlog"
)

func BenchmarkRequestLogger(b *testing.B) {
	for _, testCase := range []struct {
		name   string
		level  string
		status int
	}{
		{"info/200", "info", http.StatusOK},
		{"info/404", "info", http.StatusNotFound},
		{"debug/200", "debug", http.StatusOK},
	} {
		b.Run(testCase.name, func(b *testing.B) {
			l, err := cmdlog.New("json", testCase.level, cmdlog.WithWriter(discard{}))
			require.NoError(b, err)

			handler := requestLogger(l.Slog, true)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(testCase.status)
			}))
			req := httptest.NewRequestWithContext(b.Context(), http.MethodGet, "/v1/feature/x/eval?a=b", nil)

			b.ReportAllocs()

			for b.Loop() {
				handler.ServeHTTP(httptest.NewRecorder(), req)
			}
		})
	}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

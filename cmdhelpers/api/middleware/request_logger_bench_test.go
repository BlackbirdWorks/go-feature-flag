package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	cmdlog "github.com/blackbirdworks/semaphore/cmdhelpers/log"
)

func BenchmarkRequestLogger(b *testing.B) {
	for _, tc := range []struct {
		name   string
		level  string
		status int
	}{
		{"info/200", "info", http.StatusOK},
		{"info/404", "info", http.StatusNotFound},
		{"debug/200", "debug", http.StatusOK},
	} {
		b.Run(tc.name, func(b *testing.B) {
			l, err := cmdlog.New("json", tc.level, cmdlog.WithWriter(discard{}))
			require.NoError(b, err)
			h := requestLogger(l.Slog, true)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
			}))
			req := httptest.NewRequest(http.MethodGet, "/v1/feature/x/eval?a=b", nil)
			b.ReportAllocs()
			for b.Loop() {
				h.ServeHTTP(httptest.NewRecorder(), req)
			}
		})
	}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

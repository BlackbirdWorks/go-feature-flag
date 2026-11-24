package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmdhelpers/api/middleware"
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
			e := echo.New()
			h := middleware.RequestLogger(l.Slog, true)(func(c *echo.Context) error { return c.String(tc.status, "") })
			req := httptest.NewRequest(http.MethodGet, "/v1/feature/x/eval?a=b", nil)
			b.ReportAllocs()
			for b.Loop() {
				c := e.NewContext(req, httptest.NewRecorder())
				if handlerErr := h(c); handlerErr != nil {
					require.NoError(b, handlerErr)
				}
			}
		})
	}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

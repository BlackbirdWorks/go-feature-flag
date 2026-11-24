package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	middleware2 "github.com/blackbirdworks/semaphore/cmd/relayproxy/api/middleware"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
)

func TestVersion(t *testing.T) {
	t.Parallel()

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/whatever", nil)
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	conf := &config.Config{
		Version: "1.0.0",
	}
	middleware := middleware2.VersionHeader(middleware2.VersionHeaderConfig{
		RelayProxyConfig: conf,
	})
	handler := middleware(func(c *echo.Context) error {
		return c.String(http.StatusOK, "Authorized")
	})

	err := handler(c)
	require.NoError(t, err)
	assert.Equal(t, "1.0.0", rec.Header().Get(middleware2.VersionHeaderName))
	assert.Equal(t, "1.0.0", rec.Header().Get(middleware2.LegacyVersionHeaderName))
	assert.Equal(t, "1.0.0", rec.Header().Get("X-Semaphore-Version"))
	assert.Equal(t, "1.0.0", rec.Header().Get("X-Gofeatureflag-Version"))
}

func TestNoVersion(t *testing.T) {
	t.Parallel()

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/whatever", nil)
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	conf := &config.Config{
		Version:              "1.0.0",
		DisableVersionHeader: true,
	}
	middleware := middleware2.VersionHeader(middleware2.VersionHeaderConfig{
		Skipper: func(_ *echo.Context) bool {
			return conf.DisableVersionHeader
		},
		RelayProxyConfig: conf,
	})
	handler := middleware(func(c *echo.Context) error {
		return c.String(http.StatusOK, "Authorized")
	})

	err := handler(c)
	require.NoError(t, err)
	assert.Empty(t, rec.Header().Get("X-Gofeatureflag-Version"))
	assert.Empty(t, rec.Header().Get("X-Semaphore-Version"))
}

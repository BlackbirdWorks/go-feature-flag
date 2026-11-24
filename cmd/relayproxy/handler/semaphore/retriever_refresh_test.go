package controller_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	controller "github.com/blackbirdworks/semaphore/cmd/relayproxy/handler/semaphore"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/cmdhelpers/retrieverconf"
	"github.com/blackbirdworks/semaphore/notifier"
)

func Test_retriever_refresh_Handler_no_goff(t *testing.T) {
	t.Parallel()

	ctrl := controller.NewForceFlagsRefresh(nil, metric.Metrics{})
	e := echo.New()
	rec := httptest.NewRecorder()

	req := httptest.NewRequest(http.MethodPost, "/admin/v1/retriever/refresh", nil)
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	c := e.NewContext(req, rec)
	handlerErr := ctrl.Handler(c)
	require.Error(t, handlerErr)
	assert.Equal(
		t,
		"code=500, message=flagset manager is not initialized",
		handlerErr.Error(),
	)
}

func Test_retriever_refresh_Handler_valid(t *testing.T) {
	t.Parallel()

	// Create config for default mode
	conf := config.Config{
		Retriever: &retrieverconf.RetrieverConf{
			Kind: retrieverconf.FileRetriever,
			Path: "../../../../testdata/flag-config.yaml",
		},
	}

	flagsetManager, err := service.NewFlagsetManager(
		&conf,
		slog.New(slog.DiscardHandler),
		[]notifier.Notifier{},
		nil,
		nil,
	)
	require.NoError(t, err, "impossible to create flagset manager")

	// Get the default flagset to check refresh date
	defaultFlagset := flagsetManager.Default()
	previousRefresh := defaultFlagset.GetCacheRefreshDate()

	// the stepping clock of TestMain makes the forced refresh land on a strictly later instant
	ctrl := controller.NewForceFlagsRefresh(flagsetManager, metric.Metrics{})
	e := echo.New()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/v1/retriever/refresh", nil)
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	c := e.NewContext(req, rec)
	handlerErr := ctrl.Handler(c)
	require.NoError(t, handlerErr)

	assert.NotEqual(t, previousRefresh, defaultFlagset.GetCacheRefreshDate())
}

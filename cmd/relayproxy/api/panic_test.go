package api_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/model"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/testutils/slogutil"
)

// panickingMonitoring is a monitoring service whose health endpoint panics.
type panickingMonitoring struct{}

func (panickingMonitoring) Health() model.HealthResponse { panic("boom") }

func (panickingMonitoring) Info() (model.InfoResponse, error) { return model.InfoResponse{}, nil }

// Test_PanicIsLogged checks what a panicking handler leaves in the logs: the recovery record at the error level, and
// the access record with the 500 that was sent (the recovery is inside the access log).
func Test_PanicIsLogged(t *testing.T) {
	t.Parallel()

	logger, logs := slogutil.NewObserver(slog.LevelDebug)
	conf := testConfig(t)
	conf.Server.Port = 0
	// /health is not logged at the default level: the debug level logs everything, as the access log decides.
	conf.LogLevel = "debug"
	var monitoring service.Monitoring = panickingMonitoring{}
	srv := api.New(conf, service.Services{MonitoringService: monitoring}, logger)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/health", nil))
	assert.Equal(t, http.StatusInternalServerError, rec.Code)

	var panicRecord, accessRecord bool
	for _, entry := range logs.All() {
		switch entry.Message {
		case "panic recovered":
			panicRecord = true
			assert.Equal(t, slog.LevelError, entry.Level)
		case "Server error":
			accessRecord = true
			assert.Equal(t, slog.LevelError, entry.Level)
			assert.Equal(t, int64(http.StatusInternalServerError), entry.ContextMap()["status"])
		}
	}
	require.True(t, panicRecord, "no record of the recovered panic")
	assert.True(t, accessRecord, "no access record with the 500")
}

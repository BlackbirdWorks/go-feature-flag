package api_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
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

// okMonitoring is a monitoring service that answers.
type okMonitoring struct{}

func (okMonitoring) Health() model.HealthResponse { return model.HealthResponse{Initialized: true} }

func (okMonitoring) Info() (model.InfoResponse, error) { return model.InfoResponse{}, nil }

// Test_AccessLogShowsWhatTheClientSent checks that the access log records the request as it was sent, an unclean
// path included (cleanpath serves the cleaned one, inside the access log), with the status that was answered.
func Test_AccessLogShowsWhatTheClientSent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		target      string
		wantRequest string
	}{
		{name: "clean path", target: "/health", wantRequest: "GET /health"},
		{name: "double slash", target: "//health", wantRequest: "GET //health"},
		{name: "dot segments", target: "/a/../health?x=1", wantRequest: "GET /a/../health?x=1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			logger, logs := slogutil.NewObserver(slog.LevelDebug)
			conf := testConfig(t)
			conf.LogLevel = "debug"
			var monitoring service.Monitoring = okMonitoring{}
			srv := api.New(conf, service.Services{MonitoringService: monitoring}, logger)

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/health", nil)
			req.URL.Path = strings.SplitN(tt.target, "?", 2)[0]
			req.RequestURI = tt.target
			req.Header.Set("X-Request-ID", "req-1")
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			require.Equal(t, http.StatusOK, rec.Code, "the cleaned path is served")

			var found bool
			for _, entry := range logs.All() {
				if entry.Message != "Success" {
					continue
				}
				found = true
				fields := entry.ContextMap()
				assert.Equal(t, tt.wantRequest, fields["request"])
				assert.Equal(t, int64(http.StatusOK), fields["status"])
				assert.Equal(t, "req-1", fields["request_id"])
			}
			assert.True(t, found, "no access record")
		})
	}
}

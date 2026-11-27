package api_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/model"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/testdata/mock"
)

// initialized is the answer of the health endpoint.
func initialized() model.HealthResponse { return model.HealthResponse{Initialized: true} }

// probeService is a monitoring service that serves the probes.
type probeService struct {
	mock.MockMonitoringService
}

func (probeService) Ready() (int, model.ReadyResponse) {
	return http.StatusServiceUnavailable, model.ReadyResponse{Reason: "starting"}
}

func (probeService) Started() (int, model.ReadyResponse) {
	return http.StatusOK, model.ReadyResponse{Ready: true}
}

// TestProbeRoutes pins that the probes exist only on a monitoring service that implements them, with no key.
func TestProbeRoutes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		monitor  service.Monitoring
		name     string
		path     string
		wantBody string
		wantCode int
	}{
		{
			&probeService{mock.MockMonitoringService{HealthResponse: initialized()}},
			"livez with probes",
			"/livez",
			`{"live":true}`,
			http.StatusOK,
		},
		{
			&probeService{
				mock.MockMonitoringService{HealthResponse: initialized()},
			},
			"readyz with probes",
			"/readyz",
			`{"ready":false,"reason":"starting"}`,
			http.StatusServiceUnavailable,
		},
		{
			&probeService{mock.MockMonitoringService{HealthResponse: initialized()}},
			"startupz with probes",
			"/startupz",
			`{"ready":true}`,
			http.StatusOK,
		},
		{
			&probeService{mock.MockMonitoringService{HealthResponse: initialized()}},
			"health with probes",
			"/health",
			`{"initialized":true}`,
			http.StatusOK,
		},
		{
			&mock.MockMonitoringService{HealthResponse: initialized()},
			"no probes, no readyz",
			"/readyz",
			"",
			http.StatusNotFound,
		},
		{
			&mock.MockMonitoringService{HealthResponse: initialized()},
			"no probes, no livez",
			"/livez",
			"",
			http.StatusNotFound,
		},
		{
			&mock.MockMonitoringService{HealthResponse: initialized()},
			"no probes, health",
			"/health",
			`{"initialized":true}`,
			http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := api.New(&api.Settings{}, service.APIKeys{},
				&service.Services{MonitoringService: tt.monitor}, slog.New(slog.DiscardHandler))

			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, tt.path, nil))

			body, err := io.ReadAll(rec.Body)
			require.NoError(t, err)
			assert.Equal(t, tt.wantCode, rec.Code)

			if tt.wantBody != "" {
				assert.JSONEq(t, tt.wantBody, string(body))
			}
		})
	}
}

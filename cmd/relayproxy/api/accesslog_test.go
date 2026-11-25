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
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
	"github.com/blackbirdworks/semaphore/testhelpers/slogutil"
)

const (
	getHealthRoute = "GET /health"
)

// panicMonitoring is a monitoring service whose health endpoint panics.
type panicMonitoring struct{}

func (panicMonitoring) Health() model.HealthResponse { panic("boom") }

func (panicMonitoring) Info() (model.InfoResponse, error) { return model.InfoResponse{}, nil }

// okMonitoring is a monitoring service that answers.
type okMonitoring struct{}

func (okMonitoring) Health() model.HealthResponse { return model.HealthResponse{Initialized: true} }

func (okMonitoring) Info() (model.InfoResponse, error) { return model.InfoResponse{}, nil }

// Test_AccessLog checks what a request leaves in the logs: the access record shows the request as the client sent it
// (an unclean path included: cleanpath serves the cleaned one, inside the access log) with the status that was
// answered, and a panicking handler leaves the recovery record at the error level next to the access record of its
// 500 (the recovery is inside the access log). The lines the middleware write while the request is served (the
// recovery, the dump of the body) carry the request ID like the access record.
func Test_AccessLog(t *testing.T) {
	t.Parallel()

	tests := []struct {
		monitoring  service.Monitoring
		wantRecords map[string]slog.Level
		name        string
		target      string
		wantRequest string
		wantStatus  int
	}{
		{
			name: "clean path", monitoring: okMonitoring{}, target: healthPath, wantRequest: getHealthRoute,
			wantStatus: http.StatusOK, wantRecords: map[string]slog.Level{fixtures.LogMessageSuccess: slog.LevelDebug},
		},
		{
			name: "double slash", monitoring: okMonitoring{}, target: "//health", wantRequest: "GET //health",
			wantStatus: http.StatusOK, wantRecords: map[string]slog.Level{fixtures.LogMessageSuccess: slog.LevelDebug},
		},
		{
			name: "dot segments", monitoring: okMonitoring{}, target: "/a/../health?x=1",
			wantRequest: "GET /a/../health?x=1", wantStatus: http.StatusOK,
			wantRecords: map[string]slog.Level{fixtures.LogMessageSuccess: slog.LevelDebug},
		},
		{
			name: "panic", monitoring: panicMonitoring{}, target: healthPath, wantRequest: getHealthRoute,
			wantStatus: http.StatusInternalServerError,
			wantRecords: map[string]slog.Level{
				"panic recovered": slog.LevelError,
				serverError:       slog.LevelError,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			logger, logs := slogutil.NewObserver(slog.LevelDebug)
			conf := testConfig(t)
			// /health is not logged at the default level: the debug level logs everything.
			conf.LogLevel = "debug"
			srv := api.New(conf, &service.Services{MonitoringService: tt.monitoring}, logger)

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, healthPath, nil)
			req.URL.Path = strings.SplitN(tt.target, "?", 2)[0]
			req.RequestURI = tt.target
			req.Header.Set("X-Request-ID", "req-1")

			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			require.Equal(t, tt.wantStatus, rec.Code)

			got := map[string]slog.Level{}

			for _, entry := range logs.All() {
				fields := entry.ContextMap()
				// every line of the request, the ones of the handlers and middleware as well as the access
				// record, carries the request ID: the request logger is in the context of the request.
				assert.Equal(t, "req-1", fields["request_id"], entry.Message)

				if entry.Message == "Request info" {
					continue // the dump of the body, at the debug level
				}

				got[entry.Message] = entry.Level
				if entry.Message == "panic recovered" {
					continue
				}

				assert.Equal(t, tt.wantRequest, fields["request"])
				assert.Equal(t, int64(tt.wantStatus), fields["status"])
			}

			assert.Equal(t, tt.wantRecords, got)
		})
	}
}

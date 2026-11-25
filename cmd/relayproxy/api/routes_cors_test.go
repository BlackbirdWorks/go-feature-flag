package api_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service/stream"
	"github.com/blackbirdworks/semaphore/cmdhelpers/retrieverconf"
	"github.com/blackbirdworks/semaphore/internal/protocol"
	"github.com/blackbirdworks/semaphore/testhelpers"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

const (
	accessControlAllowOrigin = "Access-Control-Allow-Origin"
	contentTypeAuthorization = "content-type,authorization"
	vary                     = "Vary"
)

// testConfig returns a minimal HTTP relay proxy configuration. startTestServer gives it its port.
func testConfig(t *testing.T) *config.Config {
	t.Helper()

	return &config.Config{
		Retrievers: &[]retrieverconf.RetrieverConf{
			{
				Kind: fixtures.File,
				Path: fixtures.FlagConfigYAMLParentPath,
			},
		},
		Server: config.Server{
			Mode: config.ServerModeHTTP,
		},
	}
}

// startTestServer boots a relay proxy from conf and returns its base URL and, when withMonitoring is set, the
// URL of its dedicated monitoring server (empty otherwise). Both servers serve on loopback listeners that were
// bound before the server started and are kept open, so no other test can take the ports. conf gets its ports
// from them. The function waits for the monitoring server too, because it is started in its own goroutine and
// serves the endpoints the API server no longer has.
func startTestServer(t *testing.T, conf *config.Config, withMonitoring bool) (string, string) {
	t.Helper()
	ln, port := testhelpers.ListenLocal(t)
	conf.Server.Port = port
	opts := []api.Option{api.WithListener(ln)}

	var monitoringURL string

	if withMonitoring {
		monitoringLn, monitoringPort := testhelpers.ListenLocal(t)
		conf.Server.MonitoringPort = monitoringPort

		opts = append(opts, api.WithMonitoringListener(monitoringLn))
		monitoringURL = fmt.Sprintf(httpLocalhostD, monitoringPort)
	}

	log := newTestLogger(t)

	metricsV2, err := metric.NewMetrics()
	require.NoError(t, err)

	wsService := stream.NewWebsocketService()
	t.Cleanup(wsService.Close)

	flagsetManager, err := service.NewFlagsetManager(conf, log.Slog, nil, nil, nil)
	require.NoError(t, err)

	srv := newTestServer(t, conf, service.Services{
		MonitoringService: service.NewMonitoring(flagsetManager),
		WebsocketService:  wsService,
		FlagsetManager:    flagsetManager,
		Metrics:           metricsV2,
	}, log.Slog, opts...)
	go func() { srv.StartWithContext(context.Background()) }()

	t.Cleanup(func() { srv.Stop(context.Background()) })

	baseURL := fmt.Sprintf(httpLocalhostD, port)

	if withMonitoring {
		waitForServer(t, monitoringURL)
	}

	waitForServer(t, baseURL)

	return baseURL, monitoringURL
}

// doRequest performs a request with the given headers and returns the response. Callers close
// the body with t.Cleanup since every assertion here is on headers and status code.
func doRequest(t *testing.T, method, url string, headers map[string]string) *http.Response {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), method, url, http.NoBody)
	require.NoError(t, err)

	for name, value := range headers {
		req.Header.Set(name, value)
	}

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	return resp
}

// Test_CORS_SimpleRequest pins the behaviour of the default echo CORS middleware on the API
// server: any origin is allowed and no CORS header leaks on a request without an Origin.
func Test_CORS_SimpleRequest(t *testing.T) {
	t.Parallel()

	baseURL, _ := startTestServer(t, testConfig(t), false)

	tests := []struct {
		name            string
		endpoint        string
		wantAllowOrigin string
		wantStatus      int
	}{
		{name: "health endpoint", endpoint: healthPath, wantStatus: http.StatusOK, wantAllowOrigin: "*"},
		{name: "evaluation endpoint", endpoint: v1FlagChange, wantStatus: http.StatusOK, wantAllowOrigin: "*"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			t.Run("with Origin header", func(t *testing.T) {
				t.Parallel()

				resp := doRequest(t, http.MethodGet, baseURL+tt.endpoint, map[string]string{
					protocol.HeaderOrigin: httpsExampleCom,
				})
				t.Cleanup(func() { require.NoError(t, resp.Body.Close()) })
				assert.Equal(t, tt.wantStatus, resp.StatusCode)
				assert.Equal(t, tt.wantAllowOrigin, resp.Header.Get(accessControlAllowOrigin))
				assert.Contains(t, resp.Header.Values(vary), protocol.HeaderOrigin)
				assert.Empty(t, resp.Header.Get("Access-Control-Allow-Credentials"))
				assert.Empty(t, resp.Header.Get("Access-Control-Expose-Headers"))
			})

			t.Run("without Origin header", func(t *testing.T) {
				t.Parallel()

				resp := doRequest(t, http.MethodGet, baseURL+tt.endpoint, nil)
				t.Cleanup(func() { require.NoError(t, resp.Body.Close()) })
				assert.Equal(t, tt.wantStatus, resp.StatusCode)
				assert.Empty(t, resp.Header.Get(accessControlAllowOrigin))
			})
		})
	}
}

// Test_CORS_Preflight checks that a browser preflight is answered by the CORS middleware
// before the authentication middleware runs, otherwise no browser could ever call an
// authenticated endpoint of the relay proxy.
func Test_CORS_Preflight(t *testing.T) {
	t.Parallel()

	conf := testConfig(t)
	conf.AuthorizedKeys = config.APIKeys{Evaluation: []string{"my-key"}}
	conf.ForceReloadAPIKeys()
	baseURL, _ := startTestServer(t, conf, false)

	endpoints := []string{
		healthPath,
		v1Allflags,
		"/v1/feature/my-flag/eval",
		fixtures.OFREPEvaluateFlagsPath,
	}

	for _, endpoint := range endpoints {
		t.Run(endpoint, func(t *testing.T) {
			t.Parallel()

			resp := doRequest(t, http.MethodOptions, baseURL+endpoint, map[string]string{
				protocol.HeaderOrigin:       httpsExampleCom,
				accessControlRequestMethod:  http.MethodPost,
				accessControlRequestHeaders: contentTypeAuthorization,
			})
			t.Cleanup(func() { require.NoError(t, resp.Body.Close()) })
			assert.Equal(t, http.StatusNoContent, resp.StatusCode)
			assert.Equal(t, "*", resp.Header.Get(accessControlAllowOrigin))
			assert.Contains(t, resp.Header.Get("Access-Control-Allow-Methods"), http.MethodPost)
			assert.Equal(t,
				contentTypeAuthorization,
				resp.Header.Get("Access-Control-Allow-Headers"))
			// MaxAge is not configured, so browsers are never told to cache the preflight.
			assert.Empty(t, resp.Header.Get("Access-Control-Max-Age"))
		})
	}
}

// Test_CORS_MonitoringPort checks that the CORS middleware is also active on the monitoring
// server when it runs on a dedicated port.
func Test_CORS_MonitoringPort(t *testing.T) {
	t.Parallel()

	conf := testConfig(t)
	_, monitoringURL := startTestServer(t, conf, true)

	resp := doRequest(t, http.MethodGet, monitoringURL+healthPath, map[string]string{
		protocol.HeaderOrigin: httpsExampleCom,
	})
	t.Cleanup(func() { require.NoError(t, resp.Body.Close()) })
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "*", resp.Header.Get(accessControlAllowOrigin))
	assert.Contains(t, resp.Header.Values(vary), protocol.HeaderOrigin)
}

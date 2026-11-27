package api_test

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service/stream"
	"github.com/blackbirdworks/semaphore/notifier"
	"github.com/blackbirdworks/semaphore/testhelpers"
)

func TestPprofEndpointsStarts(t *testing.T) {
	t.Parallel()

	type test struct {
		name                string
		dedicatedMonitoring bool
		// checkAPIPort asks the API listener instead of the monitoring one.
		checkAPIPort       bool
		EnablePprof        bool
		expectedStatusCode int
	}

	tests := []test{
		{
			name:               "pprof is not served by a relay with one listener",
			EnablePprof:        true,
			expectedStatusCode: http.StatusNotFound,
		},
		{
			name:                "pprof is served by the ops listener",
			EnablePprof:         true,
			dedicatedMonitoring: true,
			expectedStatusCode:  http.StatusOK,
		},
		{
			name:                "pprof is not served by the api listener when the ops listener serves it",
			EnablePprof:         true,
			dedicatedMonitoring: true,
			checkAPIPort:        true,
			expectedStatusCode:  http.StatusNotFound,
		},
		{
			name:                "pprof is off by default on the ops listener",
			dedicatedMonitoring: true,
			expectedStatusCode:  http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			logger := slog.New(slog.DiscardHandler)
			ln, port := testhelpers.ListenLocal(t)
			opts := []api.Option{api.WithListener(ln)}
			monitoringPort := 0

			if tt.dedicatedMonitoring {
				var monitoringLn net.Listener

				monitoringLn, monitoringPort = testhelpers.ListenLocal(t)
				opts = append(opts, api.WithMonitoringListener(monitoringLn))
			}

			cfg := &testConf{
				settings: api.Settings{
					Mode: api.ServerModeHTTP, Port: port, MonitoringPort: monitoringPort, Pprof: tt.EnablePprof,
				},
			}

			flagsetManager := newDefaultFlags(t, []notifier.Notifier{})
			cfg.flags = flagsetManager
			apiServer := newTestServer(t, cfg, &service.Services{
				WebsocketService: stream.NewWebsocketService(),
				Metrics:          &metric.Metrics{},
			}, logger, opts...)

			portToCheck := port
			if tt.dedicatedMonitoring && !tt.checkAPIPort {
				portToCheck = monitoringPort
			}

			go apiServer.StartWithContext(context.Background())
			defer apiServer.Stop(context.Background())

			waitForServer(t, fmt.Sprintf(httpLocalhostD, portToCheck))
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
				fmt.Sprintf("http://localhost:%d/debug/pprof/heap", portToCheck), http.NoBody)
			require.NoError(t, err)
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)

			defer func() { _ = resp.Body.Close() }()

			require.Equal(t, tt.expectedStatusCode, resp.StatusCode)
		})
	}
}

// Test_VersionHeader_On_MonitoringServer checks that the version header middleware is attached
// to the monitoring server when it runs on a dedicated port, and that it honours
// disableVersionHeader on both servers.
func Test_VersionHeader_On_MonitoringServer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                 string
		expectedVersion      string
		disableVersionHeader bool
	}{
		{
			name:                 "version header enabled",
			disableVersionHeader: false,
			expectedVersion:      "1.2.3",
		},
		{
			name:                 "version header disabled",
			disableVersionHeader: true,
			expectedVersion:      "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			conf := testConfig(t)
			conf.settings.Version = "1.2.3"
			conf.settings.DisableVersionHeader = tt.disableVersionHeader
			baseURL, monitoringURL := startTestServer(t, conf, true)

			monitoringResp := doRequest(t, http.MethodGet, monitoringURL+healthPath, nil)
			t.Cleanup(func() { require.NoError(t, monitoringResp.Body.Close()) })
			assert.Equal(t, http.StatusOK, monitoringResp.StatusCode)
			assert.Equal(t,
				tt.expectedVersion,
				monitoringResp.Header.Get(xGofeatureflagVersion),
				"monitoring server")

			apiResp := doRequest(t, http.MethodGet, baseURL+v1FlagChange, nil)
			t.Cleanup(func() { require.NoError(t, apiResp.Body.Close()) })
			assert.Equal(t, http.StatusOK, apiResp.StatusCode)
			assert.Equal(t,
				tt.expectedVersion,
				apiResp.Header.Get(xGofeatureflagVersion),
				"api server")
		})
	}
}

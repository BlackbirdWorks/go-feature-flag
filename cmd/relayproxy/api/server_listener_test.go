package api_test

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/cmdhelpers/retrieverconf"
)

// TestServer_WithListener checks that the server serves on the listener it is given, which stays bound from
// the moment the caller reserved its port.
func TestServer_WithListener(t *testing.T) {
	t.Parallel()

	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port //nolint:errcheck // a tcp listener always has a *net.TCPAddr

	proxyConf := &config.Config{
		Retrievers: &[]retrieverconf.RetrieverConf{{Kind: "file", Path: "../../../testdata/flag-config.yaml"}},
		Server:     config.Server{Mode: config.ServerModeHTTP, Host: "127.0.0.1", Port: port},
	}
	log := newTestLogger(t)
	manager, err := service.NewFlagsetManager(proxyConf, log.ZapLogger, nil, nil, nil)
	require.NoError(t, err)
	t.Cleanup(manager.Close)
	s := api.New(proxyConf, service.Services{
		MonitoringService: service.NewMonitoring(manager),
		FlagsetManager:    manager,
		Metrics:           metric.Metrics{},
	}, log.ZapLogger, api.WithListener(listener))
	go s.StartWithContext(context.Background())
	t.Cleanup(func() { s.Stop(context.Background()) })

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	waitForServer(t, baseURL)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, baseURL+"/health", nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

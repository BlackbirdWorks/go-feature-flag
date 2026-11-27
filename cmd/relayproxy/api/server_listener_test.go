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
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/internal/protocol"
)

// TestServer_WithListener checks that the server serves on the listener it is given, which stays bound from
// the moment the caller reserved its port.
func TestServer_WithListener(t *testing.T) {
	t.Parallel()

	listener, err := (&net.ListenConfig{}).Listen(t.Context(), protocol.NetworkTCP, "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	tcpAddr, ok := listener.Addr().(*net.TCPAddr)
	require.True(t, ok)

	port := tcpAddr.Port

	proxyConf := &testConf{
		settings: api.Settings{Mode: api.ServerModeHTTP, Host: loopbackAddress, Port: port},
	}
	log := newTestLogger(t)
	manager := newDefaultFlags(t, nil)
	proxyConf.flags = manager
	t.Cleanup(manager.Close)

	srv := newTestServer(t, proxyConf, &service.Services{
		Metrics: &metric.Metrics{},
	}, log.Slog, api.WithListener(listener))
	go srv.StartWithContext(context.Background())

	t.Cleanup(func() { srv.Stop(context.Background()) })

	baseURL := fmt.Sprintf(localhostURLFormat, port)
	waitForServer(t, baseURL)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, baseURL+healthPath, http.NoBody)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

package api_test

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service/stream"
	"github.com/blackbirdworks/semaphore/cmdhelpers/retrieverconf"
	"github.com/blackbirdworks/semaphore/notifier"
	"github.com/blackbirdworks/semaphore/testutils"
)

const (
	apiKey2     = "?apiKey="
	flagTenantA = "flag-tenant-a"
	flagTenantB = "flag-tenant-b"
)

func TestDeprecatedAliasHeaders(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)

	listener, port := testutils.ListenLocal(t)
	cfg := &config.Config{
		Retrievers: &[]retrieverconf.RetrieverConf{
			{Kind: file2, Path: testdataFlagConfigYaml},
		},
		Server: config.Server{
			Mode: config.ServerModeHTTP,
			Port: port,
		},
	}

	flagsetManager, err := service.NewFlagsetManager(cfg, logger, []notifier.Notifier{}, nil, nil)
	require.NoError(t, err)

	sseService := stream.NewSSEService()
	defer sseService.Close()

	apiServer := newTestServer(t, cfg, service.Services{
		MonitoringService: service.NewMonitoring(flagsetManager),
		WebsocketService:  stream.NewWebsocketService(),
		SSEService:        sseService,
		FlagsetManager:    flagsetManager,
		Metrics:           metric.Metrics{},
	}, logger, api.WithListener(listener))

	go apiServer.StartWithContext(context.Background())
	defer apiServer.Stop(context.Background())

	waitForServer(t, fmt.Sprintf(httpLocalhostD, cfg.ServerPort(logger)))

	resp, err := http.Get(fmt.Sprintf("http://localhost:%d/ws/v1/flag/change", cfg.ServerPort(logger)))
	require.NoError(t, err)

	defer resp.Body.Close()

	assert.Equal(t, "true", resp.Header.Get("Deprecation"))
	assert.Equal(t, `</stream/v1/ws/flag/change>; rel="successor-version"`, resp.Header.Get("Link"))
}

// writeFlagFile writes a flag file containing a single flag, value is used to make
// every write a flag change.
func writeFlagFile(t *testing.T, path, flagName, value string) {
	t.Helper()

	content := fmt.Sprintf(`%s:
  variations:
    enabled: %q
    disabled: "disabled"
  defaultRule:
    variation: enabled
`, flagName, value)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

// readWSFrames reads the text frames of the connection in the background.
// The channel is closed when the connection is closed.
func readWSFrames(conn *websocket.Conn) <-chan string {
	frames := make(chan string, 16)
	go func() {
		defer close(frames)

		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}

			frames <- string(msg)
		}
	}()

	return frames
}

// changeFlagUntilFrame changes the flag file until a frame is received, and returns it.
// Retrying (instead of sleeping one polling interval) covers a change that is picked up
// before the websocket connection is registered.
func changeFlagUntilFrame(t *testing.T, path, flagName string, frames <-chan string) string {
	t.Helper()

	deadline := time.After(20 * time.Second)

	for {
		writeFlagFile(t, path, flagName, fmt.Sprintf("value-%d", time.Now().UnixNano()))

		select {
		case frame, ok := <-frames:
			require.True(t, ok, "websocket connection closed before receiving a frame")

			return frame
		case <-deadline:
			require.Failf(t, "no websocket frame received", "after changing %s", flagName)
		case <-time.After(3 * time.Second):
		}
	}
}

func TestWebsocketFlagChangeWithFlagsets(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tenantAFile := filepath.Join(dir, "tenant-a.yaml")
	tenantBFile := filepath.Join(dir, "tenant-b.yaml")

	writeFlagFile(t, tenantAFile, flagTenantA, "initial")
	writeFlagFile(t, tenantBFile, flagTenantB, "initial")

	flagset := func(name, apiKey, path string) config.FlagSet {
		return config.FlagSet{
			Name:            name,
			APIKeys:         []string{apiKey},
			Retrievers:      &[]retrieverconf.RetrieverConf{{Kind: file2, Path: path}},
			PollingInterval: 200,
		}
	}
	listener, port := testutils.ListenLocal(t)
	cfg := &config.Config{
		FlagSets: []config.FlagSet{
			flagset("tenant-a", "tenant-A-api-key", tenantAFile),
			flagset("tenant-b", "tenant-B-api-key", tenantBFile),
		},
		AuthorizedKeys: config.APIKeys{Admin: []string{"admin-api-key"}},
		Server: config.Server{
			Mode: config.ServerModeHTTP,
			Port: port,
		},
	}
	cfg.ForceReloadAPIKeys()

	logger := slog.New(slog.DiscardHandler)

	wsService := stream.NewWebsocketService()

	t.Cleanup(func() { wsService.Close() })

	flagsetManager, err := service.NewFlagsetManager(cfg, logger, []notifier.Notifier{}, nil, wsService)
	require.NoError(t, err)
	t.Cleanup(func() { flagsetManager.Close() })

	apiServer := newTestServer(t, cfg, service.Services{
		MonitoringService: service.NewMonitoring(flagsetManager),
		WebsocketService:  wsService,
		FlagsetManager:    flagsetManager,
		Metrics:           metric.Metrics{},
	}, logger, api.WithListener(listener))
	go apiServer.StartWithContext(context.Background())

	t.Cleanup(func() { apiServer.Stop(context.Background()) })
	waitForServer(t, fmt.Sprintf(httpLocalhostD, cfg.ServerPort(logger)))
	wsURL := fmt.Sprintf("ws://localhost:%d", cfg.ServerPort(logger))

	endpoints := []struct {
		name string
		path string
	}{
		{name: "stream endpoint", path: streamV1Ws},
		{name: "legacy alias", path: wsV1Flag},
	}

	for _, tt := range endpoints {
		t.Run(tt.name+": changes are only sent to the clients of the flagset", func(t *testing.T) {
			t.Parallel()

			dial := func(apiKey string) <-chan string {
				conn, resp, dialErr := websocket.DefaultDialer.Dial(wsURL+tt.path+apiKey2+apiKey, nil)
				require.NoError(t, dialErr)

				_ = resp.Body.Close()

				t.Cleanup(func() { _ = conn.Close() })

				return readWSFrames(conn)
			}
			tenantAFrames := dial("tenant-A-api-key")
			tenantBFrames := dial("tenant-B-api-key")

			// The tenant-b client receives the change of its flagset.
			frame := changeFlagUntilFrame(t, tenantBFile, flagTenantB, tenantBFrames)
			assert.Contains(t, frame, flagTenantB)
			assert.NotContains(t, frame, flagTenantA)

			// The tenant-b change has been broadcast: if it had been sent to the tenant-a
			// client, it would be the first frame read here, before the tenant-a change.
			frame = changeFlagUntilFrame(t, tenantAFile, flagTenantA, tenantAFrames)
			assert.Contains(t, frame, flagTenantA)
			assert.NotContains(t, frame, flagTenantB)
		})
	}

	rejected := []struct {
		name   string
		apiKey string
		want   int
	}{
		{name: "key not associated to a flagset", apiKey: "admin-api-key", want: http.StatusBadRequest},
		{name: "unknown key", apiKey: "unknown-api-key", want: http.StatusUnauthorized},
	}
	for _, endpoint := range endpoints {
		for _, tt := range rejected {
			t.Run(endpoint.name+": "+tt.name+" is rejected before the upgrade", func(t *testing.T) {
				t.Parallel()

				ws, resp, dialErr := websocket.DefaultDialer.Dial(wsURL+endpoint.path+apiKey2+tt.apiKey, nil)
				if ws != nil {
					_ = ws.Close()
				}

				require.ErrorIs(t, dialErr, websocket.ErrBadHandshake)

				defer func() { _ = resp.Body.Close() }()

				assert.Equal(t, tt.want, resp.StatusCode)
			})
		}
	}
}

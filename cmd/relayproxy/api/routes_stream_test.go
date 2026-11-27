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
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service/stream"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/testdata/mock"
	"github.com/blackbirdworks/semaphore/notifier"
	"github.com/blackbirdworks/semaphore/testhelpers"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

const (
	apiKeyQuery = "?apiKey="
	flagTenantA = "flag-tenant-a"
	flagTenantB = "flag-tenant-b"
)

func TestDeprecatedAliasHeaders(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)

	listener, port := testhelpers.ListenLocal(t)
	cfg := &testConf{
		settings: api.Settings{Mode: api.ServerModeHTTP, Port: port},
	}

	flagsetManager := newDefaultFlags(t, []notifier.Notifier{})
	cfg.flags = flagsetManager

	sseService := stream.NewSSEService()
	defer sseService.Close()

	apiServer := newTestServer(t, cfg, &service.Services{
		WebsocketService: stream.NewWebsocketService(),
		SSEService:       sseService,
		Metrics:          &metric.Metrics{},
	}, logger, api.WithListener(listener))

	go apiServer.StartWithContext(context.Background())
	defer apiServer.Stop(context.Background())

	waitForServer(t, fmt.Sprintf(httpLocalhostD, cfg.settings.Port))

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
		fmt.Sprintf("http://localhost:%d/ws/v1/flag/change", cfg.settings.Port), http.NoBody)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
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

// wsNotifier forwards the changes of a flag set to the websocket service, as the relay does for each flag set.
type wsNotifier struct {
	service stream.WebsocketService
	flagset string
}

func (n wsNotifier) Notify(diff notifier.DiffCache) error {
	n.service.BroadcastFlagChanges(n.flagset, diff)

	return nil
}

func TestWebsocketFlagChangeWithFlagsets(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tenantAFile := filepath.Join(dir, "tenant-a.yaml")
	tenantBFile := filepath.Join(dir, "tenant-b.yaml")

	writeFlagFile(t, tenantAFile, flagTenantA, "initial")
	writeFlagFile(t, tenantBFile, flagTenantB, "initial")

	logger := slog.New(slog.DiscardHandler)
	wsService := stream.NewWebsocketService()

	t.Cleanup(func() { wsService.Close() })

	flagset := func(name, apiKey, path string) mock.Named {
		cfg := mock.FileConfig(path)
		cfg.PollingInterval = 200 * time.Millisecond
		cfg.Notifiers = []notifier.Notifier{wsNotifier{service: wsService, flagset: name}}

		return mock.Named{Name: name, APIKeys: []string{apiKey}, Config: cfg}
	}
	listener, port := testhelpers.ListenLocal(t)
	cfg := &testConf{
		settings: api.Settings{Mode: api.ServerModeHTTP, Port: port},
		keys: mock.Keys{
			Evaluation: []string{"tenant-A-api-key", "tenant-B-api-key"},
			Admin:      []string{"admin-api-key"},
		},
		flags: mock.NewNamed(t,
			flagset("tenant-a", "tenant-A-api-key", tenantAFile),
			flagset("tenant-b", "tenant-B-api-key", tenantBFile),
		),
	}

	apiServer := newTestServer(t, cfg, &service.Services{
		WebsocketService: wsService,
		Metrics:          &metric.Metrics{},
	}, logger, api.WithListener(listener))
	go apiServer.StartWithContext(context.Background())

	t.Cleanup(func() { apiServer.Stop(context.Background()) })
	waitForServer(t, fmt.Sprintf(httpLocalhostD, cfg.settings.Port))
	wsURL := fmt.Sprintf("ws://localhost:%d", cfg.settings.Port)

	endpoints := []struct {
		name string
		path string
	}{
		{name: "stream endpoint", path: streamV1Ws},
		{name: "legacy alias", path: fixtures.WebsocketFlagChangePath},
	}

	for _, tt := range endpoints {
		t.Run(tt.name+": changes are only sent to the clients of the flagset", func(t *testing.T) {
			t.Parallel()

			dial := func(apiKey string) <-chan string {
				conn, resp, dialErr := websocket.DefaultDialer.Dial(wsURL+tt.path+apiKeyQuery+apiKey, nil)
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

				ws, resp, dialErr := websocket.DefaultDialer.Dial(wsURL+endpoint.path+apiKeyQuery+tt.apiKey, nil)
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

package controller_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	controller "github.com/blackbirdworks/semaphore/cmd/relayproxy/handler/semaphore"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service/stream"
	"github.com/blackbirdworks/semaphore/modules/core/flag"
	"github.com/blackbirdworks/semaphore/notifier"
)

var (
	errNoFlagSetAssociated = errors.New("no flag set associated to the API key")
)

// newDefaultFlagsetMgr is a flagset manager in default mode (no flagsets configured).
func newDefaultFlagsetMgr() *mockFlagsetManagerSSE {
	return &mockFlagsetManagerSSE{flagsetName: "default", isDefault: true}
}

func Test_websocket_flag_change(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		flagChange  notifier.DiffCache
		wantMessage string
	}{
		{
			name: "Update 1 flag",
			flagChange: notifier.DiffCache{
				Deleted: nil,
				Added:   nil,
				Updated: map[string]notifier.DiffUpdated{
					"my-flag": {
						Before: &flag.InternalFlag{
							Variations: &map[string]*any{
								"A": new(any(true)),
								"B": new(any(false)),
							},
							DefaultRule: &flag.Rule{
								VariationResult: new("A"),
							},
						},
						After: &flag.InternalFlag{
							Variations: &map[string]*any{
								"A": new(any(true)),
								"B": new(any(false)),
							},
							DefaultRule: &flag.Rule{
								VariationResult: new("B"),
							},
						},
					},
				},
			},
			wantMessage: `{"added":null,"deleted":null,"updated":{"my-flag":{"old_value":{"variations":{"A":true,"B":false` +
				`},"defaultRule":{"variation":"A"}},"new_value":{"variations":{"A":true,"B":false},"defaultRule":{"variation` +
				`":"B"}}}}}`,
		},
		{
			name: "Update remove and add flag at the same time",
			flagChange: notifier.DiffCache{
				Deleted: map[string]flag.Flag{
					"flag-1": &flag.InternalFlag{
						Variations: &map[string]*any{
							"A": new(any(true)),
							"B": new(any(false)),
						},
						DefaultRule: &flag.Rule{
							VariationResult: new("A"),
						},
					},
				},
				Added: map[string]flag.Flag{
					"flag-2": &flag.InternalFlag{
						Variations: &map[string]*any{
							"A": new(any(true)),
							"B": new(any(false)),
						},
						DefaultRule: &flag.Rule{
							VariationResult: new("A"),
						},
					},
				},
				Updated: map[string]notifier.DiffUpdated{
					"my-flag": {
						Before: &flag.InternalFlag{
							Variations: &map[string]*any{
								"A": new(any(true)),
								"B": new(any(false)),
							},
							DefaultRule: &flag.Rule{
								VariationResult: new("A"),
							},
						},
						After: &flag.InternalFlag{
							Variations: &map[string]*any{
								"A": new(any(true)),
								"B": new(any(false)),
							},
							DefaultRule: &flag.Rule{
								VariationResult: new("B"),
							},
						},
					},
				},
			},
			wantMessage: `{"added":{"flag-2":{"variations":{"A":true,"B":false},"defaultRule":{"variation":"A"}}},"deleted` +
				`":{"flag-1":{"variations":{"A":true,"B":false},"defaultRule":{"variation":"A"}}},"updated":{"my-flag":{"old` +
				`_value":{"variations":{"A":true,"B":false},"defaultRule":{"variation":"A"}},"new_value":{"variations":{"A":` +
				`true,"B":false},"defaultRule":{"variation":"B"}}}}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Create context with timeout for the entire test
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			websocketService := stream.NewWebsocketService()
			defer func() {
				websocketService.Close()
				// Wait for cleanup to complete to avoid leaking goroutines in tests.
				if err := websocketService.WaitForCleanup(5 * time.Second); err != nil {
					assert.NoError(t, err, "websocket service cleanup failed")
				}
			}()

			log := slog.New(slog.DiscardHandler)
			ctrl := controller.NewWsFlagChange(websocketService, newDefaultFlagsetMgr(), log)

			e := echo.New()
			e.GET("/ws/v1/flag/change", ctrl.Handler)
			testServer := httptest.NewServer(e)
			defer testServer.Close()

			url := "ws" + strings.TrimPrefix(testServer.URL, "http") + "/ws/v1/flag/change"

			// Create websocket connection with timeout
			dialer := &websocket.Dialer{
				HandshakeTimeout: 10 * time.Second,
			}
			ws, _, err := dialer.DialContext(ctx, url, nil)
			require.NoError(t, err, "Failed to connect to WebSocket")
			defer func() {
				ws.SetWriteDeadline(time.Now().Add(5 * time.Second))
				_ = ws.Close()
			}()

			// Set read deadline to prevent hanging
			err = ws.SetReadDeadline(time.Now().Add(10 * time.Second))
			require.NoError(t, err)

			// The first message the client receives is the one that proves that its connection is registered
			// in the service: broadcast until it arrives instead of sleeping.
			received, firstMsg := readFirstMessage(ws)
			broadcastUntilRegistered(t, websocketService, tt.flagChange, firstMsg)

			assert.JSONEq(t, tt.wantMessage, string(<-received))
		})
	}
}

// readFirstMessage reads the connection in the background. The first message is sent on the first channel and
// then its arrival is signalled on the second one, which is what broadcastUntilRegistered waits for.
func readFirstMessage(ws *websocket.Conn) (<-chan []byte, <-chan struct{}) {
	received := make(chan []byte, 1)
	firstMsg := make(chan struct{}, 1)
	go func() {
		first := true
		for {
			_, msg, err := ws.ReadMessage()
			if err != nil {
				return
			}
			if first {
				first = false
				received <- msg
				firstMsg <- struct{}{}
			}
		}
	}()

	return received, firstMsg
}

// broadcastUntilRegistered broadcasts until the client confirms receipt of a
// message (signalled on firstMsg), which proves the connection is in the
// service's client map. This replaces a fixed sleep, which is fragile on loaded
// CI runners: if broadcasts fire before Register() completes they hit an empty
// map and the test passes vacuously without exercising the write path.
func broadcastUntilRegistered(
	t *testing.T,
	svc stream.WebsocketService,
	diff notifier.DiffCache,
	firstMsg <-chan struct{},
) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		svc.BroadcastFlagChanges("default", diff)
		select {
		case <-firstMsg:
			return
		case <-deadline:
			require.Fail(t, "connection never registered: no broadcast reached the client")
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// Test_websocket_concurrent_writes is a regression test for issue #5463.
// The ping loop and the flag-change broadcast both write to the same websocket
// connection. Without synchronization gorilla/websocket panics with
// "concurrent write to websocket connection" and crashes the relay proxy.
// Run with -race to surface the data race if the writes are not serialized.
// Observable outcome: no panic and no data race (race detector) while the ping loop and the broadcasts write the same
// connection.
func Test_websocket_concurrent_writes(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	websocketService := stream.NewWebsocketService()
	defer func() {
		websocketService.Close()
		if err := websocketService.WaitForCleanup(5 * time.Second); err != nil {
			assert.NoError(t, err, "websocket service cleanup failed")
		}
	}()

	ctrl := controller.NewWsFlagChange(websocketService, newDefaultFlagsetMgr(), slog.New(slog.DiscardHandler))

	e := echo.New()
	e.GET("/ws/v1/flag/change", ctrl.Handler)
	testServer := httptest.NewServer(e)
	defer testServer.Close()

	url := "ws" + strings.TrimPrefix(testServer.URL, "http") + "/ws/v1/flag/change"

	dialer := &websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	ws, _, err := dialer.DialContext(ctx, url, nil)
	require.NoError(t, err, "Failed to connect to WebSocket")
	defer func() {
		_ = ws.SetWriteDeadline(time.Now().Add(5 * time.Second))
		_ = ws.Close()
	}()

	// Drain incoming messages so the server-side writes can make progress, and
	// signal the first one so we know the connection is registered.
	clientDone := make(chan struct{})
	firstMsg := make(chan struct{}, 1)
	go func() {
		defer close(clientDone)
		first := true
		for {
			if _, _, readMessageErr := ws.ReadMessage(); readMessageErr != nil {
				return
			}
			if first {
				first = false
				firstMsg <- struct{}{}
			}
		}
	}()

	diff := notifier.DiffCache{
		Updated: map[string]notifier.DiffUpdated{
			"my-flag": {
				Before: &flag.InternalFlag{
					DefaultRule: &flag.Rule{VariationResult: new("A")},
				},
				After: &flag.InternalFlag{
					DefaultRule: &flag.Rule{VariationResult: new("B")},
				},
			},
		},
	}

	// Confirm the connection is registered before the flood; a fixed sleep is
	// fragile on loaded CI runners (see broadcastUntilRegistered).
	broadcastUntilRegistered(t, websocketService, diff, firstMsg)

	// Fire many broadcasts concurrently while the 1s ping loop is running.
	// This reproduces both concurrency vectors: broadcast vs ping and
	// broadcast vs broadcast (BroadcastFlagChanges only holds an RLock).
	const broadcasters = 8
	const broadcastsEach = 50
	var wg sync.WaitGroup
	for range broadcasters {
		wg.Go(func() {
			for range broadcastsEach {
				websocketService.BroadcastFlagChanges("default", diff)
			}
		})
	}
	wg.Wait()

	// Close the connection and make sure the client goroutine stops.
	_ = ws.Close()
	select {
	case <-clientDone:
	case <-time.After(5 * time.Second):
		require.Fail(t, "client read loop did not stop")
	}
}

// Test_websocket_legacy_handler verifies the deprecated LegacyHandler endpoint
// still delegates to Handler and pushes flag changes to the client.
func Test_websocket_legacy_handler(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	websocketService := stream.NewWebsocketService()
	defer func() {
		websocketService.Close()
		if err := websocketService.WaitForCleanup(5 * time.Second); err != nil {
			assert.NoError(t, err, "websocket service cleanup failed")
		}
	}()

	ctrl := controller.NewWsFlagChange(websocketService, newDefaultFlagsetMgr(), slog.New(slog.DiscardHandler))

	e := echo.New()
	e.GET("/ws/v1/flag/change", ctrl.LegacyHandler)
	testServer := httptest.NewServer(e)
	defer testServer.Close()

	url := "ws" + strings.TrimPrefix(testServer.URL, "http") + "/ws/v1/flag/change"
	dialer := &websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	ws, _, err := dialer.DialContext(ctx, url, nil)
	require.NoError(t, err, "Failed to connect to WebSocket")
	defer func() { _ = ws.Close() }()
	require.NoError(t, ws.SetReadDeadline(time.Now().Add(10*time.Second)))

	diff := notifier.DiffCache{
		Updated: map[string]notifier.DiffUpdated{
			"my-flag": {
				Before: &flag.InternalFlag{
					DefaultRule: &flag.Rule{VariationResult: new("A")},
				},
				After: &flag.InternalFlag{
					DefaultRule: &flag.Rule{VariationResult: new("B")},
				},
			},
		},
	}
	// the first message the client receives proves that its connection is registered in the service
	received, firstMsg := readFirstMessage(ws)
	broadcastUntilRegistered(t, websocketService, diff, firstMsg)

	expected, err := json.Marshal(diff)
	require.NoError(t, err)
	assert.JSONEq(t, string(expected), string(<-received))
}

// Test_websocket_flagset_scoping verifies that, with flagsets, a client is
// registered on the flagset of its API key and only receives its changes.
func Test_websocket_flagset_scoping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		handler        func(*controller.WSFlagChange) echo.HandlerFunc
		wantFlag       string
		wantAbsentFlag string
	}{
		{
			name:           "handler",
			handler:        func(c *controller.WSFlagChange) echo.HandlerFunc { return c.Handler },
			wantFlag:       "right-flag",
			wantAbsentFlag: "wrong-flag",
		},
		{
			name:           "legacy handler",
			handler:        func(c *controller.WSFlagChange) echo.HandlerFunc { return c.LegacyHandler },
			wantFlag:       "right-flag",
			wantAbsentFlag: "wrong-flag",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			websocketService := stream.NewWebsocketService()
			defer websocketService.Close()

			flagsetMgr := &mockFlagsetManagerSSE{flagsetName: "flagsetA", isDefault: false}
			ctrl := controller.NewWsFlagChange(websocketService, flagsetMgr, slog.New(slog.DiscardHandler))

			e := echo.New()
			e.GET("/ws", tt.handler(ctrl))
			testServer := httptest.NewServer(e)
			defer testServer.Close()

			url := "ws" + strings.TrimPrefix(testServer.URL, "http") + "/ws?apiKey=key-a"
			ws, _, err := websocket.DefaultDialer.DialContext(ctx, url, nil)
			require.NoError(t, err)
			defer func() { _ = ws.Close() }()

			msgs := make(chan string, 64)
			go func() {
				defer close(msgs)
				for {
					_, msg, readMessageErr := ws.ReadMessage()
					if readMessageErr != nil {
						return
					}
					msgs <- string(msg)
				}
			}()

			diff := func(flagName string) notifier.DiffCache {
				return notifier.DiffCache{Added: map[string]flag.Flag{flagName: &flag.InternalFlag{}}}
			}

			// Broadcast until a message is received, it proves the connection is registered.
			deadline := time.After(5 * time.Second)
			registered := false
			for !registered {
				websocketService.BroadcastFlagChanges("flagsetA", diff("probe-flag"))
				select {
				case <-msgs:
					registered = true
				case <-deadline:
					require.Fail(t, "connection never registered: no broadcast reached the client")
				case <-time.After(20 * time.Millisecond):
				}
			}

			// The connection is registered and the writes are ordered: if the flagsetB
			// broadcast reached the client, it would be read before the flagsetA one.
			websocketService.BroadcastFlagChanges("flagsetB", diff(tt.wantAbsentFlag))
			websocketService.BroadcastFlagChanges("flagsetA", diff(tt.wantFlag))
			for {
				select {
				case msg, ok := <-msgs:
					require.True(t, ok, "websocket connection closed before receiving the flag change")
					require.NotContains(t, msg, tt.wantAbsentFlag)
					if strings.Contains(msg, tt.wantFlag) {
						return
					}
				case <-deadline:
					require.Fail(t, "the flagsetA broadcast never reached the client")
				}
			}
		})
	}
}

// Test_websocket_flagset_not_resolved verifies that the connection is rejected
// before the websocket upgrade when no flagset can be resolved from the API key.
func Test_websocket_flagset_not_resolved(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		handler    func(*controller.WSFlagChange) echo.HandlerFunc
		query      string
		wantStatus int
	}{
		{
			name:       "handler: no apiKey",
			handler:    func(c *controller.WSFlagChange) echo.HandlerFunc { return c.Handler },
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "handler: apiKey not associated to a flagset",
			handler:    func(c *controller.WSFlagChange) echo.HandlerFunc { return c.Handler },
			query:      "?apiKey=unknown",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "legacy handler: no apiKey",
			handler:    func(c *controller.WSFlagChange) echo.HandlerFunc { return c.LegacyHandler },
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "legacy handler: apiKey not associated to a flagset",
			handler:    func(c *controller.WSFlagChange) echo.HandlerFunc { return c.LegacyHandler },
			query:      "?apiKey=unknown",
			wantStatus: http.StatusBadRequest,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			websocketService := stream.NewWebsocketService()
			defer websocketService.Close()

			flagsetMgr := &mockFlagsetManagerSSE{
				isDefault: false,
				err:       errNoFlagSetAssociated,
			}
			ctrl := controller.NewWsFlagChange(websocketService, flagsetMgr, slog.New(slog.DiscardHandler))

			e := echo.New()
			e.GET("/ws", tt.handler(ctrl))
			testServer := httptest.NewServer(e)
			defer testServer.Close()

			url := "ws" + strings.TrimPrefix(testServer.URL, "http") + "/ws" + tt.query
			ws, resp, err := websocket.DefaultDialer.Dial(url, nil)
			if ws != nil {
				_ = ws.Close()
			}
			require.ErrorIs(t, err, websocket.ErrBadHandshake)
			defer func() { _ = resp.Body.Close() }()
			assert.Equal(t, tt.wantStatus, resp.StatusCode)
		})
	}
}

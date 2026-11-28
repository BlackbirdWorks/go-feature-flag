package semaphore

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/blackbirdworks/huma-middleware/middleware/logging"
	"github.com/gorilla/websocket"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/helper"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service/stream"
	reqlog "github.com/blackbirdworks/semaphore/cmdhelpers/api/middleware"
	"github.com/blackbirdworks/semaphore/cmdhelpers/cmdlog"
)

// threadSafeConn serializes all writes to a single websocket connection.
// gorilla/websocket only supports one concurrent writer, and both the ping
// loop and the flag-change broadcast write to the same connection. Without
// this guard the relay proxy panics with "concurrent write to websocket
// connection" (see issue #5463).
type threadSafeConn struct {
	conn writeConn
	// now is the clock the deadlines are measured on; nil is the wall clock, which the network deadlines run on.
	now func() time.Time
	// deadline is the time the network gets to take one message.
	deadline time.Duration
	mu       sync.Mutex
}

// writeConn is what the writers use of a websocket connection: *websocket.Conn implements it.
type writeConn interface {
	SetWriteDeadline(deadline time.Time) error
	WriteJSON(message any) error
	WriteMessage(messageType int, data []byte) error
	Close() error
}

// WriteJSON satisfies stream.WebsocketConnector (used by BroadcastFlagChanges).
func (c *threadSafeConn) WriteJSON(message any) error {
	err := c.write(func(conn writeConn) error { return conn.WriteJSON(message) })
	if err != nil {
		return fmt.Errorf("write websocket JSON: %w", err)
	}

	return nil
}

// Close closes the connection: the relay does it when the access of the client ends.
func (c *threadSafeConn) Close() error {
	err := c.conn.Close()
	if err != nil {
		return fmt.Errorf("close websocket: %w", err)
	}

	return nil
}

// ping sends a ping control message to the client.
func (c *threadSafeConn) ping() error {
	err := c.write(func(conn writeConn) error { return conn.WriteMessage(websocket.PingMessage, nil) })
	if err != nil {
		return fmt.Errorf("write websocket ping: %w", err)
	}

	return nil
}

// write runs one write on the connection, alone, with a fresh deadline that is set right before it. A deadline stays
// on the connection after the write, so every writer (messages and control frames) sets its own: none runs on the
// one that an earlier write left behind.
func (c *threadSafeConn) write(send func(writeConn) error) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// real time on purpose: a network deadline is enforced by the runtime poller, not by clock.Now. The service
	// closes a client that misses its own timeout, this deadline ends the write that the close would otherwise have
	// to interrupt.
	deadline := c.deadline
	if deadline <= 0 {
		deadline = writeDeadline
	}

	now := c.now
	if now == nil {
		now = time.Now
	}

	_ = c.conn.SetWriteDeadline(now().Add(deadline))

	return send(c.conn)
}

// writeDeadline is the time the network gets to take one message.
const writeDeadline = 10 * time.Second

// defaultPingInterval is how often a keep-alive ping is sent to the client.
const defaultPingInterval = 1 * time.Second

// WSFlagChange is the implementation of the controller.
type WSFlagChange struct {
	websocketService stream.WebsocketService
	flagsetManager   service.FlagSetResolver
	upgrader         websocket.Upgrader
	// pingInterval is the keep-alive ping period; defaults to defaultPingInterval.
	pingInterval time.Duration
}

// NewWsFlagChange is the constructor to create a new controller to handle websocket
// request to be notified about flag changes.
func NewWsFlagChange(
	websocketService stream.WebsocketService,
	flagsetManager service.FlagSetResolver,
) *WSFlagChange {
	return &WSFlagChange{
		websocketService: websocketService,
		flagsetManager:   flagsetManager,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(_ *http.Request) bool {
				return true
			},
		},
		pingInterval: defaultPingInterval,
	}
}

// LegacyHandler is the entry point for the websocket endpoint to get notified when a flag has been edited.
func (f *WSFlagChange) LegacyHandler(w http.ResponseWriter, r *http.Request) error {
	// This handler is deprecated and we keep it for the documentation.
	return f.Handler(w, r)
}

// Handler is the entry point for the websocket endpoint to get notified when a flag has been edited.
func (f *WSFlagChange) Handler(w http.ResponseWriter, r *http.Request) error {
	// The flagset is resolved before the upgrade, so a failure is a plain HTTP error.
	apiKey := r.URL.Query().Get("apiKey")

	flagsetName, err := helper.RequestFlagSetName(r.Context(), f.flagsetManager, apiKey)
	if err != nil {
		return fmt.Errorf(
			"binding the request of the flag change websocket: %w",
			helper.NewHTTPError(http.StatusBadRequest, err.Error()),
		)
	}

	conn, err := f.upgrader.Upgrade(w, r, nil)
	if err != nil {
		// the upgrader sent the error response (a 400 or a 426): only the access log has the reason.
		reqlog.RecordError(r.Context(), err)

		return nil
	}
	defer func() { _ = conn.Close() }()

	// Wrap the connection so that all writes (ping loop + flag-change broadcast)
	// are serialized through a single mutex. gorilla/websocket only supports one
	// concurrent writer.
	safeConn := &threadSafeConn{conn: conn, deadline: writeDeadline}

	if apiKey == "" {
		f.websocketService.RegisterAnonymous(safeConn, flagsetName)
	} else {
		f.websocketService.Register(safeConn, flagsetName)
	}

	defer f.websocketService.Deregister(safeConn)

	logger := logging.FromContext(r.Context())
	logger.LogAttrs(
		r.Context(),
		slog.LevelDebug,
		"registering new websocket connection",
		slog.Any("connection", conn),
	)

	// Create context for cancellation
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Start the ping pong loop
	go f.pingPongLoop(ctx, safeConn)

	const readDeadline = 60 * time.Second

	conn.SetPongHandler(func(string) error {
		// real time on purpose: a network deadline is enforced by the runtime poller, not by clock.Now.
		_ = conn.SetReadDeadline(time.Now().Add(readDeadline))

		return nil
	})

	_ = conn.SetReadDeadline(time.Now().Add(readDeadline))
	for {
		// ReadMessage is needed to process control messages like pongs and close messages.
		_, _, readMessageErr := conn.ReadMessage()
		if readMessageErr != nil {
			logger.LogAttrs(r.Context(), slog.LevelDebug,
				"closing websocket connection",
				cmdlog.Err(readMessageErr),
				slog.Any("connection", conn),
			)

			return nil
		}
	}
}

// pingPongLoop is a keep-alive call to the client.
// It calls the client to ensure that the connection is still active.
// If the ping is not working we are closing the session.
func (f *WSFlagChange) pingPongLoop(ctx context.Context, conn *threadSafeConn) {
	// Ping interval duration (fall back to the default for a zero-valued struct
	// so time.NewTicker never panics on a non-positive duration).
	pingInterval := f.pingInterval
	if pingInterval <= 0 {
		pingInterval = defaultPingInterval
	}
	// Create a ticker to send pings at regular intervals
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// Send a ping message to the client
			err := conn.ping()
			if err != nil {
				logging.FromContext(ctx).LogAttrs(ctx, slog.LevelDebug,
					"closing websocket connection",
					cmdlog.Err(err),
					slog.Any("connection", conn),
				)

				return
			}
		case <-ctx.Done():
			logging.FromContext(ctx).DebugContext(ctx, "stopping ping pong loop")

			return
		}
	}
}

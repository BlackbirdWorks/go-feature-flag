package semaphore

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service/stream"
)

// newWSConnPair stands up a throwaway websocket server and returns the
// server-side and client-side *websocket.Conn so the unexported write paths
// (threadSafeConn / pingPongLoop) can be tested directly.
func newWSConnPair(t *testing.T) (*websocket.Conn, *websocket.Conn) {
	t.Helper()

	upgrader := websocket.Upgrader{}
	serverConnCh := make(chan *websocket.Conn, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}

		serverConnCh <- c
	}))
	t.Cleanup(srv.Close)

	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	client, resp, err := websocket.DefaultDialer.Dial(url, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, resp.Body.Close()) })

	server := <-serverConnCh

	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})

	return server, client
}

// Observable outcome: the ping frame reaches the client (signalled on a channel), and a ping on a closed connection
// returns an error.
func Test_threadSafeConn_ping(t *testing.T) {
	t.Parallel()

	server, client := newWSConnPair(t)

	pinged := make(chan struct{}, 1)

	client.SetPingHandler(func(string) error {
		select {
		case pinged <- struct{}{}:
		default:
		}

		return nil
	})
	// A reader is required for the client to process incoming control frames.
	go func() {
		for {
			_, _, err := client.ReadMessage()
			if err != nil {
				return
			}
		}
	}()

	tsConn := &threadSafeConn{conn: server}

	// Happy path: the ping reaches the client.
	require.NoError(t, tsConn.ping())

	select {
	case <-pinged:
	case <-time.After(2 * time.Second):
		require.Fail(t, "client never received the ping")
	}

	// Error path: writing to a closed connection returns an error.
	_ = server.Close()

	assert.Error(t, tsConn.ping())
}

// Observable outcome: the loop sends a ping, then returns once its context is cancelled (the channel done is closed).
func Test_pingPongLoop_pings_then_stops_on_context(t *testing.T) {
	t.Parallel()

	server, client := newWSConnPair(t)

	pinged := make(chan struct{}, 8)

	client.SetPingHandler(func(string) error {
		select {
		case pinged <- struct{}{}:
		default:
		}

		return nil
	})

	go func() {
		for {
			_, _, err := client.ReadMessage()
			if err != nil {
				return
			}
		}
	}()

	ctrl := NewWsFlagChange(stream.NewWebsocketService(), nil)
	ctrl.pingInterval = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		ctrl.pingPongLoop(ctx, &threadSafeConn{conn: server})
		close(done)
	}()

	select {
	case <-pinged:
	case <-time.After(2 * time.Second):
		require.Fail(t, "ping loop never sent a ping")
	}

	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		require.Fail(t, "ping loop did not stop when the context was cancelled")
	}
}

// Observable outcome: the loop returns by itself when the ping cannot be written (the channel done is closed).
func Test_pingPongLoop_stops_on_write_error(t *testing.T) {
	t.Parallel()

	server, client := newWSConnPair(t)
	// Close both ends so the next write fails immediately.
	_ = client.Close()
	_ = server.Close()

	ctrl := NewWsFlagChange(stream.NewWebsocketService(), nil)
	ctrl.pingInterval = 5 * time.Millisecond

	done := make(chan struct{})

	go func() {
		ctrl.pingPongLoop(context.Background(), &threadSafeConn{conn: server})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		require.Fail(t, "ping loop did not stop after a write error")
	}
}

// Test_pingPongLoop_zero_interval_uses_default ensures a zero-valued interval
// falls back to the default rather than panicking [time.NewTicker].
// Observable outcome: the loop starts and stops with a zero interval instead of panicking in [time.NewTicker] (done is
// closed).
func Test_pingPongLoop_zero_interval_uses_default(t *testing.T) {
	t.Parallel()

	server, _ := newWSConnPair(t)

	ctrl := NewWsFlagChange(stream.NewWebsocketService(), nil)
	ctrl.pingInterval = 0 // force the fallback branch

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		ctrl.pingPongLoop(ctx, &threadSafeConn{conn: server})
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		require.Fail(t, "ping loop did not start/stop with a zero interval")
	}
}

// recordingConn is a connection that records the write deadline each write ran on.
type recordingConn struct {
	deadline  time.Time
	deadlines []time.Time
}

func (c *recordingConn) SetWriteDeadline(deadline time.Time) error {
	c.deadline = deadline

	return nil
}

func (c *recordingConn) WriteJSON(any) error {
	c.deadlines = append(c.deadlines, c.deadline)

	return nil
}

func (c *recordingConn) WriteMessage(int, []byte) error {
	c.deadlines = append(c.deadlines, c.deadline)

	return nil
}

func (*recordingConn) Close() error { return nil }

// Every write, a message or a ping, runs on a deadline that was set right before it, on the clock of the connection:
// the deadline of an earlier write is never the one of a later write, however long the connection was quiet.
func Test_threadSafeConn_deadline_is_fresh_for_every_write(t *testing.T) {
	t.Parallel()

	const deadline = 10 * time.Second

	start := time.Date(2026, time.November, 27, 12, 0, 0, 0, time.UTC)
	quiet := start.Add(time.Hour)
	later := quiet.Add(time.Minute)

	clock := []time.Time{start, quiet, later}
	conn := &recordingConn{}
	safe := &threadSafeConn{
		conn: conn, deadline: deadline,
		now: func() time.Time {
			next := clock[0]
			clock = clock[1:]

			return next
		},
	}

	require.NoError(t, safe.WriteJSON(map[string]int{"change": 1}))
	require.NoError(t, safe.ping(), "the ping after the quiet time")
	require.NoError(t, safe.WriteJSON(map[string]int{"change": 2}))

	assert.Equal(t, []time.Time{start.Add(deadline), quiet.Add(deadline), later.Add(deadline)}, conn.deadlines)
}

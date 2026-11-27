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

// A message, then a quiet time longer than the write deadline, then a ping and the next message: the deadline of the
// first write must not be the one of the later writes. A subscriber that is quiet for a while stays connected.
func Test_threadSafeConn_deadline_is_fresh_for_every_write(t *testing.T) {
	t.Parallel()

	const (
		deadline = 30 * time.Millisecond
		bound    = 10 * time.Second
	)

	server, client := newWSConnPair(t)
	conn := &threadSafeConn{conn: server, deadline: deadline}

	pinged := make(chan struct{}, 1)

	client.SetPingHandler(func(string) error {
		pinged <- struct{}{}

		return nil
	})

	messages := make(chan string, 2)
	readFailed := make(chan error, 1)

	go func() {
		for {
			_, msg, err := client.ReadMessage()
			if err != nil {
				readFailed <- err

				return
			}

			messages <- string(msg)
		}
	}()

	awaitMessage := func(want string) {
		t.Helper()

		select {
		case got := <-messages:
			assert.JSONEq(t, want, got)
		case err := <-readFailed:
			require.FailNow(t, "the connection ended", "%v", err)
		case <-time.After(bound):
			require.FailNow(t, "the message did not arrive")
		}
	}

	require.NoError(t, conn.WriteJSON(map[string]int{"change": 1}))
	awaitMessage(`{"change":1}`)

	// the quiet time: longer than the write deadline, a real wait on the clock the network deadline runs on
	quietFrom := time.Now()

	require.Eventually(t, func() bool { return time.Since(quietFrom) > 3*deadline }, bound, time.Millisecond)

	require.NoError(t, conn.ping(), "the ping runs on a deadline of its own")

	select {
	case <-pinged:
	case <-time.After(bound):
		require.FailNow(t, "the ping did not reach the client")
	}

	require.NoError(t, conn.WriteJSON(map[string]int{"change": 2}), "the next message runs on a deadline of its own")
	awaitMessage(`{"change":2}`)
}

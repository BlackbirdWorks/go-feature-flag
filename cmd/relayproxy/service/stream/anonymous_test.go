package stream_test

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service/stream"
	"github.com/blackbirdworks/semaphore/notifier"
)

const (
	anonBound = 5 * time.Second
	setA      = "a"
	setB      = "b"
)

// closableConn counts what a websocket connection is told and whether it is closed.
type closableConn struct {
	mu     sync.Mutex
	writes int
	closed int
}

func (c *closableConn) WriteJSON(any) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.writes++

	return nil
}

func (c *closableConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.closed++

	return nil
}

func (c *closableConn) counts() (int, int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.writes, c.closed
}

// DisconnectAnonymous closes the connections without a key whose flag set is not the kept one, and only them: no
// message reaches a closed connection, and the connections with a key are never touched.
func TestWebsocketDisconnectAnonymous(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		keep            string
		wantAnonAClosed int
		wantAnonBClosed int
		wantAnonAWrites int
		wantAnonBWrites int
		wantConnections int
	}{
		{name: "the access is cleared", keep: "", wantAnonAClosed: 1, wantAnonBClosed: 1, wantConnections: 1},
		{
			name: "the access moves to flag set a", keep: setA, wantAnonBClosed: 1, wantAnonAWrites: 1,
			wantConnections: 2,
		},
		{
			name: "the access moves to flag set b", keep: setB, wantAnonAClosed: 1, wantAnonBWrites: 1,
			wantConnections: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := stream.NewWebsocketService()
			anonA, anonB, keyedA := &closableConn{}, &closableConn{}, &closableConn{}

			svc.RegisterAnonymous(anonA, setA)
			svc.RegisterAnonymous(anonB, setB)
			svc.Register(keyedA, setA)

			svc.DisconnectAnonymous(tt.keep)
			svc.BroadcastFlagChanges(setA, sensitiveDiffCache())
			svc.BroadcastFlagChanges(setB, sensitiveDiffCache())

			writes, closed := anonA.counts()
			assert.Equal(t, tt.wantAnonAClosed, closed, "closes of the anonymous connection of a")
			assert.Equal(t, tt.wantAnonAWrites, writes, "messages to the anonymous connection of a")

			writes, closed = anonB.counts()
			assert.Equal(t, tt.wantAnonBClosed, closed, "closes of the anonymous connection of b")
			assert.Equal(t, tt.wantAnonBWrites, writes, "messages to the anonymous connection of b")

			writes, closed = keyedA.counts()
			assert.Equal(t, 0, closed, "a connection with a key is never closed")
			assert.Equal(t, 1, writes, "a connection with a key gets its change")
			assert.Equal(t, tt.wantConnections, svc.Connections())
		})
	}
}

// DisconnectAnonymous ends the streams of the clients without a key whose flag set is not the kept one; the stream of
// a client with a key goes on.
func TestSSEDisconnectAnonymous(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		keep            string
		wantConnections int
	}{
		{name: "the access is cleared", keep: "", wantConnections: 1},
		{name: "the access moves to flag set a", keep: setA, wantConnections: 2},
		{name: "the access moves to flag set b", keep: setB, wantConnections: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := stream.NewSSEService()
			t.Cleanup(svc.Close)

			subscribed := make(chan string, 3)

			svc.SetOnSubscribe(func(id string) { subscribed <- id })

			srv := httptest.NewServer(http.HandlerFunc(svc.ServeHTTP))
			t.Cleanup(srv.Close)

			connect := func(streamID string) *bufio.Scanner {
				req, err := http.NewRequestWithContext(
					t.Context(),
					http.MethodGet,
					srv.URL+"?stream="+streamID,
					http.NoBody,
				)
				require.NoError(t, err)

				resp, err := http.DefaultClient.Do(req)
				require.NoError(t, err)
				t.Cleanup(func() { _ = resp.Body.Close() })

				return bufio.NewScanner(resp.Body)
			}

			connect(stream.AnonymousStreamPrefix + setA)
			connect(stream.AnonymousStreamPrefix + setB)
			keyed := connect(setA)

			for range 3 {
				select {
				case <-subscribed:
				case <-time.After(anonBound):
					require.FailNow(t, "a client did not subscribe")
				}
			}

			require.Equal(t, 3, svc.Connections())

			svc.DisconnectAnonymous(tt.keep)

			require.Eventually(t, func() bool { return svc.Connections() == tt.wantConnections }, anonBound,
				5*time.Millisecond, "the closed streams are released")

			require.NoError(t, svc.BroadcastFlagChanges(setA, notifier.DiffCache{}))

			got := ""

			for keyed.Scan() {
				if line := keyed.Text(); strings.HasPrefix(line, "data:") {
					got = line

					break
				}
			}

			assert.Contains(t, got, "refetchEvaluation", "the client with a key gets its change")
		})
	}
}

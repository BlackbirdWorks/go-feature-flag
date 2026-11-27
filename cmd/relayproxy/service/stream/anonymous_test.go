package stream_test

import (
	"bufio"
	"errors"
	"net/http"
	"net/http/httptest"
	"runtime"
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

			lines := make(chan string, 1)

			go func() {
				for keyed.Scan() {
					if line := keyed.Text(); strings.HasPrefix(line, "data:") {
						lines <- line

						return
					}
				}
			}()

			select {
			case got := <-lines:
				assert.Contains(t, got, "refetchEvaluation", "the client with a key gets its change")
			case <-time.After(anonBound):
				require.FailNow(t, "the client with a key did not get its change")
			}
		})
	}
}

// stuckConn is a client whose connection never reads: its write waits until the connection is closed.
type stuckConn struct {
	entered chan struct{}
	gate    chan struct{}
	once    sync.Once
}

func newStuckConn() *stuckConn {
	return &stuckConn{entered: make(chan struct{}, 8), gate: make(chan struct{})}
}

func (c *stuckConn) WriteJSON(any) error {
	c.entered <- struct{}{}

	<-c.gate

	return errClosedStuck
}

//nolint:unparam // the Close of a connection
func (c *stuckConn) Close() error {
	c.once.Do(func() { close(c.gate) })

	return nil
}

var errClosedStuck = errors.New("the stuck connection was closed")

// A client that never reads must not hold anything of the service: the next broadcast ends within the write timeout,
// the stuck client is closed and removed, the others get the change, and a disconnect or a registration never waits
// for the write.
func TestWebsocketStuckClientHoldsNothing(t *testing.T) {
	t.Parallel()

	const writeTimeout = 20 * time.Millisecond

	tests := []struct {
		name            string
		timeout         time.Duration
		wantBroadcast   bool
		wantStuckClosed bool
		wantGoodWrites  int
	}{
		{
			name: "the write timeout ends the broadcast", timeout: writeTimeout, wantBroadcast: true,
			wantStuckClosed: true, wantGoodWrites: 1,
		},
		{
			name: "the service stays usable while the write waits", timeout: time.Hour, wantBroadcast: false,
			wantStuckClosed: false, wantGoodWrites: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := stream.NewWebsocketService(stream.WithWriteTimeout(tt.timeout))
			stuck, good, anon := newStuckConn(), &closableConn{}, &closableConn{}

			svc.Register(stuck, setA)
			svc.Register(good, setA)
			svc.RegisterAnonymous(anon, setB)

			t.Cleanup(func() { _ = stuck.Close() })

			broadcast := make(chan struct{})

			go func() {
				svc.BroadcastFlagChanges(setA, sensitiveDiffCache())
				close(broadcast)
			}()

			select {
			case <-stuck.entered:
			case <-time.After(anonBound):
				require.FailNow(t, "the broadcast did not reach the stuck client")
			}

			// while the write is pending: a disconnect and a registration return, and the revoked client is closed
			done := make(chan struct{})

			go func() {
				svc.DisconnectAnonymous("")
				svc.Register(&closableConn{}, setB)
				close(done)
			}()

			select {
			case <-done:
			case <-time.After(anonBound):
				require.FailNow(t, "the service waited for a client that does not read")
			}

			_, anonClosed := anon.counts()
			assert.Equal(t, 1, anonClosed, "the revoked client is closed at once")

			if tt.wantBroadcast {
				select {
				case <-broadcast:
				case <-time.After(anonBound):
					require.FailNow(t, "the broadcast did not end within the write timeout")
				}
			}

			select {
			case <-stuck.gate:
				assert.True(t, tt.wantStuckClosed, "the stuck client was closed")
			default:
				assert.False(t, tt.wantStuckClosed, "the stuck client must have been closed")
			}

			writes, _ := good.counts()
			if tt.wantBroadcast {
				assert.Equal(t, tt.wantGoodWrites, writes, "the other client got the change")
			}

			// stuck + good + the client registered while the write was pending, and the revoked one is gone
			if tt.wantBroadcast {
				assert.Equal(t, 3-1, svc.Connections(), "the stuck client is removed")
			} else {
				assert.Equal(t, 3, svc.Connections(), "the stuck client is still registered while the write waits")
			}
		})
	}
}

// libraryGoroutines is the number of goroutines of the process that run code of the SSE library.
func libraryGoroutines() int {
	buf := make([]byte, 1<<20)
	buf = buf[:runtime.Stack(buf, true)]

	count := 0

	for goroutine := range strings.SplitSeq(string(buf), "\n\n") {
		if strings.Contains(goroutine, "github.com/r3labs/sse") {
			count++
		}
	}

	return count
}

// Revoking the anonymous access of N connected clients leaves no goroutine of the SSE library behind: the library
// would keep one blocked per client if the stream were removed under them.
//
//nolint:paralleltest // it counts the goroutines of the process: it runs while the parallel tests are not started
func TestSSEDisconnectAnonymousLeavesNoGoroutine(t *testing.T) {
	const clients = 5

	baseline := libraryGoroutines()

	svc := stream.NewSSEService(stream.WithSSEHeartbeatInterval(0))
	subscribed := make(chan string, clients)

	svc.SetOnSubscribe(func(id string) { subscribed <- id })

	srv := httptest.NewServer(http.HandlerFunc(svc.ServeHTTP))

	for range clients {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
			srv.URL+"?stream="+stream.AnonymousStreamPrefix+setA, http.NoBody)
		require.NoError(t, err)

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		t.Cleanup(func() { _ = resp.Body.Close() })
	}

	for range clients {
		select {
		case <-subscribed:
		case <-time.After(anonBound):
			require.FailNow(t, "a client did not subscribe")
		}
	}

	svc.DisconnectAnonymous("")

	require.Eventually(t, func() bool { return svc.Connections() == 0 }, anonBound, 5*time.Millisecond,
		"the revoked streams are released")

	srv.CloseClientConnections()
	srv.Close()
	svc.Close()

	assert.Eventually(t, func() bool { return libraryGoroutines() <= baseline }, anonBound, 10*time.Millisecond,
		"goroutines of the SSE library are left after the revocation")
}

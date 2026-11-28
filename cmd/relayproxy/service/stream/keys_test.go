package stream_test

import (
	"bufio"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
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
	keyOne    = "key-one"
	keyTwo    = "key-two"
	keyThree  = "key-three"
	keyNewOne = "key-one-rotated"
	setOne    = "set-one"
	setTwo    = "set-two"
	connOne   = "one"
	connTwo   = "two"
	connThree = "three"
)

// revocation is a change of the keys of the runtime: the keys that route to a flag set after it, and the clients (by
// name) that must be disconnected.
type revocation struct {
	routes      map[string]string
	name        string
	wantRevoked []string
}

// revocations are the cases of both services. The clients: connOne (keyOne, flag set setOne), connTwo (keyTwo,
// setOne) and connThree (keyThree, setTwo).
func revocations() []revocation {
	return []revocation{
		{
			name:        "nothing changed",
			routes:      map[string]string{keyOne: setOne, keyTwo: setOne, keyThree: setTwo},
			wantRevoked: nil,
		},
		{
			name:        "a key is removed, the other keys of its flag set are not touched",
			routes:      map[string]string{keyTwo: setOne, keyThree: setTwo},
			wantRevoked: []string{connOne},
		},
		{
			name:        "a flag set is withdrawn",
			routes:      map[string]string{keyThree: setTwo},
			wantRevoked: []string{connOne, connTwo},
		},
		{
			name:        "a rotation with an overlap disconnects nobody",
			routes:      map[string]string{keyOne: setOne, keyNewOne: setOne, keyTwo: setOne, keyThree: setTwo},
			wantRevoked: nil,
		},
		{
			name:        "the rotation ends: the old key goes",
			routes:      map[string]string{keyNewOne: setOne, keyTwo: setOne, keyThree: setTwo},
			wantRevoked: []string{connOne},
		},
		{
			name:        "a key that moved to another flag set goes",
			routes:      map[string]string{keyOne: setTwo, keyTwo: setOne, keyThree: setTwo},
			wantRevoked: []string{connOne},
		},
	}
}

// registerActive registers a connection with a key and activates it, as the handler does after its second check.
func registerActive(tb testing.TB, svc *stream.WebsocketServiceImpl, conn stream.WebsocketConnector, set, key string) {
	tb.Helper()

	svc.RegisterKeyed(conn, set, key)
	require.True(tb, svc.Activate(conn))
}

func routesOf(routes map[string]string) stream.KeyRoutes {
	return func(yield func(string, string) bool) {
		for key, set := range routes {
			if !yield(key, set) {
				return
			}
		}
	}
}

// DisconnectRevoked takes the connections whose key does not route to their flag set out of the list at once, hands
// back what closes them, and touches no other connection: not the other keys, not the connections without a key.
func TestWebsocketDisconnectRevoked(t *testing.T) {
	t.Parallel()

	for _, tt := range revocations() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := stream.NewWebsocketService()
			conns := map[string]*closableConn{
				connOne: {}, connTwo: {}, connThree: {}, "anonymous": {}, "unkeyed": {},
			}
			registerActive(t, svc, conns[connOne], setOne, keyOne)
			registerActive(t, svc, conns[connTwo], setOne, keyTwo)
			registerActive(t, svc, conns[connThree], setTwo, keyThree)
			svc.RegisterAnonymous(conns["anonymous"], setOne)
			svc.Register(conns["unkeyed"], setOne)

			closers := svc.DisconnectRevoked(routesOf(tt.routes))

			assert.Len(t, closers, len(tt.wantRevoked), "one close per revoked connection")
			assert.Equal(t, 5-len(tt.wantRevoked), svc.Connections(), "the revoked are out of the list")

			for name, conn := range conns {
				_, closed := conn.counts()
				assert.Zero(t, closed, "%s: nothing is closed before the caller runs the closes", name)
			}

			for _, closeOne := range closers {
				closeOne()
			}

			svc.BroadcastFlagChanges(setOne, notifier.DiffCache{})

			for name, conn := range conns {
				writes, closed := conn.counts()
				revoked := contains(tt.wantRevoked, name)

				assert.Equal(t, boolInt(revoked), closed, "%s: closed", name)
				assert.Equal(
					t,
					boolInt(!revoked && name != connThree),
					writes,
					"%s: a revoked client gets nothing",
					name,
				)
			}
		})
	}
}

func contains(list []string, name string) bool {
	return slices.Contains(list, name)
}

func boolInt(value bool) int {
	if value {
		return 1
	}

	return 0
}

// A service with no connection that has a key does not read the routes, and a deregistered connection is not revoked.
func TestWebsocketDisconnectRevokedWithoutKeyedConnections(t *testing.T) {
	t.Parallel()

	svc := stream.NewWebsocketService()
	svc.Register(&closableConn{}, setOne)

	read := false
	routes := func(func(string, string) bool) { read = true }

	assert.Empty(t, svc.DisconnectRevoked(routes))
	assert.False(t, read, "the routes are not read when no connection has a key")

	gone := &closableConn{}
	registerActive(t, svc, gone, setOne, keyOne)
	svc.Deregister(gone)

	assert.Empty(t, svc.DisconnectRevoked(routesOf(nil)), "a connection that left is not revoked a second time")
}

// DisconnectRevoked ends the streams of the clients whose key does not route to their flag set, and only those: the
// other clients of the flag sets keep their stream and get the next change.
func TestSSEDisconnectRevoked(t *testing.T) {
	t.Parallel()

	for _, tt := range revocations() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := stream.NewSSEService()
			t.Cleanup(svc.Close)

			subscribed := make(chan string, 8)

			svc.SetOnSubscribe(func(id string) { subscribed <- id })

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				svc.ServeKeyed(w, r, r.URL.Query().Get("key"), func() bool { return true })
			}))
			t.Cleanup(srv.Close)

			clients := map[string]*sseClient{}

			for name, conn := range map[string][2]string{
				connOne: {keyOne, setOne}, connTwo: {keyTwo, setOne}, connThree: {keyThree, setTwo},
			} {
				clients[name] = connectSSE(t, srv.URL, conn[0], conn[1])
			}

			for range 3 {
				select {
				case <-subscribed:
				case <-time.After(anonBound):
					require.FailNow(t, "a client did not subscribe")
				}
			}

			svc.DisconnectRevoked(routesOf(tt.routes))

			awaitEnded(t, clients, tt.wantRevoked)

			require.Eventually(t, func() bool { return svc.Connections() == 3-len(tt.wantRevoked) }, anonBound,
				5*time.Millisecond, "the revoked streams are released")

			// a change after the revocation: the clients that are still valid get it, the revoked ones are gone
			require.NoError(t, svc.BroadcastFlagChanges(setOne, notifier.DiffCache{}))
			require.NoError(t, svc.BroadcastFlagChanges(setTwo, notifier.DiffCache{}))

			awaitChange(t, clients, tt.wantRevoked)
		})
	}
}

// awaitEnded waits for the end of the streams of the revoked clients.
func awaitEnded(t *testing.T, clients map[string]*sseClient, revoked []string) {
	t.Helper()

	for name, client := range clients {
		if !contains(revoked, name) {
			continue
		}

		select {
		case <-client.ended:
		case <-time.After(anonBound):
			assert.Fail(t, "the stream of a revoked client did not end", name)
		}
	}
}

// awaitChange waits for the change on every client that is still valid.
func awaitChange(t *testing.T, clients map[string]*sseClient, revoked []string) {
	t.Helper()

	for name, client := range clients {
		if contains(revoked, name) {
			continue
		}

		select {
		case line := <-client.lines:
			assert.Contains(t, line, "refetchEvaluation", name)
		case <-client.ended:
			assert.Fail(t, "the stream of a client that is still valid ended", name)
		case <-time.After(anonBound):
			assert.Fail(t, "the client that is still valid did not get the change", name)
		}
	}
}

// sseClient reads a stream of the service: the data lines it gets, and the end of its body.
type sseClient struct {
	lines chan string
	ended chan struct{}
}

func connectSSE(t *testing.T, base, key, set string) *sseClient {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+"?stream="+set+"&key="+key, http.NoBody)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })

	client := &sseClient{lines: make(chan string, 8), ended: make(chan struct{})}

	go func() {
		defer close(client.ended)

		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			if line := scanner.Text(); strings.HasPrefix(line, "data:") {
				client.lines <- line
			}
		}
	}()

	return client
}

// The SSE stream is still one per flag set: a client with a key and a client without share the publication, and
// revoking the keys does not change what a change costs.
func TestSSEServeKeyedRefusesAKeyRevokedWhileItRegisters(t *testing.T) {
	t.Parallel()

	svc := stream.NewSSEService()
	t.Cleanup(svc.Close)

	recorder := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/?stream="+setOne, http.NoBody)

	svc.ServeKeyed(recorder, req, keyOne, func() bool { return false })

	assert.Zero(t, svc.Connections())
	assert.Empty(t, recorder.Body.String(), "nothing is served to a key that is not valid anymore")
}

// A connection that registered with a key receives nothing until it is activated, and a revocation reaches it while it
// waits: a key that a swap revoked before the registration never gets a change, whatever the order of the events.
func TestWebsocketKeyedConnectionReceivesNothingUntilActivated(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// what happens to the connection after the registration and a first change
		revoke, leave, activate bool
		wantActivated           bool
		wantWrites              int
		wantClosed              int
	}{
		{name: "waiting: no change reaches it"},
		{name: "activated: it gets the next change", activate: true, wantActivated: true, wantWrites: 1},
		{name: "revoked while it waits: it never gets one and is closed", revoke: true, activate: true, wantClosed: 1},
		{name: "left while it waits: it can not be activated", leave: true, activate: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := stream.NewWebsocketService()
			conn := &closableConn{}
			svc.RegisterKeyed(conn, setOne, keyOne)

			// a change while it waits: the diff of a private flag must not reach it
			svc.BroadcastFlagChanges(setOne, notifier.DiffCache{})

			writes, _ := conn.counts()
			require.Zero(t, writes, "nothing is written to a connection that waits")

			switch {
			case tt.revoke:
				for _, closeOne := range svc.DisconnectRevoked(routesOf(nil)) {
					closeOne()
				}
			case tt.leave:
				svc.Deregister(conn)
			}

			activated := tt.activate && svc.Activate(conn)

			svc.BroadcastFlagChanges(setOne, notifier.DiffCache{})

			writes, closed := conn.counts()

			assert.Equal(t, tt.wantActivated, activated)
			assert.Equal(t, tt.wantWrites, writes)
			assert.Equal(t, tt.wantClosed, closed)
		})
	}
}

// blockedResponse is a response whose writes wait for the network: a write returns only when its write deadline is
// reached, the way a client that does not read holds a connection.
type blockedResponse struct {
	header   http.Header
	deadline chan time.Time
	entered  chan struct{}
	armed    chan struct{}
	set      chan time.Time
	once     sync.Once
	armOnce  sync.Once
}

func newBlockedResponse() *blockedResponse {
	return &blockedResponse{
		header: http.Header{}, deadline: make(chan time.Time, 64), entered: make(chan struct{}),
		armed: make(chan struct{}), set: make(chan time.Time, 64),
	}
}

func (b *blockedResponse) Header() http.Header { return b.header }
func (*blockedResponse) WriteHeader(int)       {}

// Write blocks until a deadline in the past is set, as a write on a full socket ends when its deadline passes.
func (b *blockedResponse) Write(data []byte) (int, error) {
	b.once.Do(func() { close(b.entered) })

	for deadline := range b.set {
		// the deadline of a revocation is in the past: any other deadline is a write that may go on
		if deadline.Unix() <= 1 {
			return 0, errBlockedWrite
		}

		b.armOnce.Do(func() { close(b.armed) })
	}

	return len(data), nil
}

func (*blockedResponse) Flush() {}

//nolint:unparam // the SetWriteDeadline of a response
func (b *blockedResponse) SetWriteDeadline(deadline time.Time) error {
	b.set <- deadline

	return nil
}

var errBlockedWrite = errors.New("the write deadline passed")

// An SSE stream whose client does not read ends when its key is revoked, and does not wait for the network: the
// revocation sets an immediate write deadline. Every write of a stream sets a deadline of its own first.
func TestSSEBlockedWriteEndsAtRevocation(t *testing.T) {
	t.Parallel()

	svc := stream.NewSSEService()
	t.Cleanup(svc.Close)

	subscribed := make(chan string, 1)

	svc.SetOnSubscribe(func(id string) { subscribed <- id })

	response := newBlockedResponse()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/?stream="+setOne, http.NoBody)
	served := make(chan struct{})

	go func() {
		defer close(served)

		svc.ServeKeyed(response, req, keyOne, func() bool { return true })
	}()

	// the first write of the stream (the connected comment) is the one that blocks: the client does not read
	select {
	case <-response.entered:
	case <-time.After(anonBound):
		require.FailNow(t, "the stream did not write")
	}

	svc.DisconnectRevoked(routesOf(nil))

	select {
	case <-served:
	case <-time.After(anonBound):
		require.FailNow(t, "the stream of the revoked client did not end while its write was blocked")
	}

	assert.Zero(t, svc.Connections())

	select {
	case <-response.armed:
	default:
		assert.Fail(t, "the write of the stream had no deadline of its own")
	}
}

// deadlineResponse records the write deadlines set on a response, in order.
type deadlineResponse struct {
	*httptest.ResponseRecorder

	deadlines []time.Time
	mu        sync.Mutex
}

func (d *deadlineResponse) SetWriteDeadline(deadline time.Time) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.deadlines = append(d.deadlines, deadline)

	return nil
}

func (d *deadlineResponse) last() time.Time {
	d.mu.Lock()
	defer d.mu.Unlock()

	if len(d.deadlines) == 0 {
		return time.Time{}
	}

	return d.deadlines[len(d.deadlines)-1]
}

// The teardown of a stream (public flags withdrawn, key revoked) keeps a write deadline: the server writes the last
// chunk after the handler returns, and a client that does not read must not hold the connection with it.
func TestSSETeardownKeepsAWriteDeadline(t *testing.T) {
	t.Parallel()

	tests := []struct {
		teardown     func(*stream.SSEServiceImpl)
		name         string
		streamID     string
		keyed        bool
		wantDeadline bool
	}{
		{
			name: "public flags withdrawn", streamID: stream.AnonymousStreamPrefix + setOne,
			teardown: func(svc *stream.SSEServiceImpl) { svc.DisconnectAnonymous("") }, wantDeadline: true,
		},
		{
			name: "key revoked", streamID: setOne, keyed: true,
			teardown:     func(svc *stream.SSEServiceImpl) { svc.DisconnectRevoked(routesOf(nil)) },
			wantDeadline: true,
		},
		{
			name: "client left", streamID: setOne, wantDeadline: true,
			teardown: func(*stream.SSEServiceImpl) {},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := stream.NewSSEService(stream.WithSSEHeartbeatInterval(0))
			t.Cleanup(svc.Close)

			subscribed := make(chan string, 1)

			svc.SetOnSubscribe(func(id string) { subscribed <- id })

			ctx, cancel := context.WithCancel(t.Context())
			t.Cleanup(cancel)

			response := &deadlineResponse{ResponseRecorder: httptest.NewRecorder()}
			req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/?stream="+tt.streamID, http.NoBody)
			served := make(chan struct{})

			go func() {
				defer close(served)

				if tt.keyed {
					svc.ServeKeyed(response, req, keyOne, func() bool { return true })

					return
				}

				svc.ServeHTTP(response, req)
			}()

			select {
			case <-subscribed:
			case <-time.After(anonBound):
				require.FailNow(t, "the client did not subscribe")
			}

			tt.teardown(svc)
			cancel()

			select {
			case <-served:
			case <-time.After(anonBound):
				require.FailNow(t, "the stream did not end")
			}

			assert.Equal(t, tt.wantDeadline, !response.last().IsZero(), "the last write of the stream has a deadline")
		})
	}
}

// A quiet stream is not reset between two events: the write deadline is cleared after each write, so a stream that is
// idle for longer than the deadline still gets the next change. HTTP/2 resets a stream whose deadline expires, HTTP/1.1
// does not care; both are served.
func TestSSEQuietStreamKeepsItsConnection(t *testing.T) {
	t.Parallel()

	const deadline = 30 * time.Millisecond

	tests := []struct {
		name         string
		http2        bool
		wantProtocol int
	}{
		{name: "HTTP/1.1", http2: false, wantProtocol: 1},
		{name: "HTTP/2", http2: true, wantProtocol: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := stream.NewSSEService(stream.WithWriteTimeout(deadline), stream.WithSSEHeartbeatInterval(0))
			t.Cleanup(svc.Close)

			subscribed := make(chan string, 1)

			svc.SetOnSubscribe(func(id string) { subscribed <- id })

			srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				svc.ServeKeyed(w, r, keyOne, func() bool { return true })
			}))
			srv.EnableHTTP2 = tt.http2

			if tt.http2 {
				srv.StartTLS()
			} else {
				srv.Start()
			}

			t.Cleanup(srv.Close)

			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"?stream="+setOne, http.NoBody)
			require.NoError(t, err)

			resp, err := srv.Client().Do(req)
			require.NoError(t, err)
			t.Cleanup(func() { _ = resp.Body.Close() })

			assert.Equal(t, tt.wantProtocol, resp.ProtoMajor)

			client := &sseClient{lines: make(chan string, 8), ended: make(chan struct{})}

			go func() {
				defer close(client.ended)

				scanner := bufio.NewScanner(resp.Body)
				for scanner.Scan() {
					if line := scanner.Text(); strings.HasPrefix(line, "data:") {
						client.lines <- line
					}
				}
			}()

			select {
			case <-subscribed:
			case <-time.After(anonBound):
				require.FailNow(t, "the client did not subscribe")
			}

			// the stream is idle for several deadlines: a deadline left armed would have reset it by now
			<-time.After(5 * deadline)

			require.NoError(t, svc.BroadcastFlagChanges(setOne, notifier.DiffCache{}))

			select {
			case line := <-client.lines:
				assert.Contains(t, line, "refetchEvaluation")
			case <-client.ended:
				assert.Fail(t, "the quiet stream was reset")
			case <-time.After(anonBound):
				assert.Fail(t, "the quiet stream did not get the change")
			}
		})
	}
}

package stream_test

import (
	"bufio"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
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
			svc.RegisterKeyed(conns[connOne], setOne, keyOne)
			svc.RegisterKeyed(conns[connTwo], setOne, keyTwo)
			svc.RegisterKeyed(conns[connThree], setTwo, keyThree)
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
	svc.RegisterKeyed(gone, setOne, keyOne)
	svc.Deregister(gone)

	assert.Empty(t, svc.DisconnectRevoked(routesOf(nil)), "a connection that left is not revoked a second time")
}

// DisconnectRevoked ends the streams of the clients whose key does not route to their flag set, and only those: the
// other clients of the flag set keep their stream and get the next change.
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

			ended := map[string]chan struct{}{}
			scanners := map[string]*bufio.Scanner{}

			connect := func(name, key, set string) {
				req, err := http.NewRequestWithContext(
					t.Context(), http.MethodGet, srv.URL+"?stream="+set+"&key="+key, http.NoBody,
				)
				require.NoError(t, err)

				resp, err := http.DefaultClient.Do(req)
				require.NoError(t, err)
				t.Cleanup(func() { _ = resp.Body.Close() })

				scanners[name] = bufio.NewScanner(resp.Body)
				ended[name] = make(chan struct{})

				go func() {
					// the body ends when the handler returns
					_, _ = io.Copy(io.Discard, resp.Body)

					close(ended[name])
				}()
			}

			connect(connOne, keyOne, setOne)
			connect(connTwo, keyTwo, setOne)
			connect(connThree, keyThree, setTwo)

			for range 3 {
				select {
				case <-subscribed:
				case <-time.After(anonBound):
					require.FailNow(t, "a client did not subscribe")
				}
			}

			svc.DisconnectRevoked(routesOf(tt.routes))

			for name, done := range ended {
				if !contains(tt.wantRevoked, name) {
					continue
				}

				select {
				case <-done:
				case <-time.After(anonBound):
					assert.Fail(t, "the stream of a revoked client did not end", name)
				}
			}

			require.Eventually(t, func() bool { return svc.Connections() == 3-len(tt.wantRevoked) }, anonBound,
				5*time.Millisecond, "the revoked streams are released")

			for name, done := range ended {
				if contains(tt.wantRevoked, name) {
					continue
				}

				select {
				case <-done:
					assert.Fail(t, "the stream of a client that is still valid ended", name)
				default:
				}
			}
		})
	}
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

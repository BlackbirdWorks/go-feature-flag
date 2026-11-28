package corehttp_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/corehttp"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service/stream"
)

const (
	keyedSetA   = "set-a"
	keyedSetB   = "set-b"
	keyedBound  = 5 * time.Second
	keyOld      = "key-old"
	keyNew      = "key-new"
	keyOther    = "key-other"
	connOld     = "old"
	connOther   = "other"
	connWithout = "without"
)

// keyedRuntime is a runtime that routes the keys to the flag sets (key to flag set name).
func keyedRuntime(routes map[string]string) *flagruntime.Runtime {
	built := &flagruntime.Runtime{Status: &flagruntime.Status{}, ByKey: map[string]*flagruntime.FlagSet{}}

	for key, set := range routes {
		built.ByKey[key] = &flagruntime.FlagSet{Name: set}
	}

	return built
}

// closeSignal is a websocket connection that tells when it is closed, and can hold its close.
type closeSignal struct {
	closed chan struct{}
	hold   chan struct{}
}

func (*closeSignal) WriteJSON(any) error { return nil }

//nolint:unparam // the Close of a connection
func (c *closeSignal) Close() error {
	if c.hold != nil {
		<-c.hold
	}

	close(c.closed)

	return nil
}

func newCloseSignal() *closeSignal { return &closeSignal{closed: make(chan struct{})} }

// Revalidate closes the streams of the keys that do not route to their flag set anymore, with the same rights as a new
// request: a removed key, a withdrawn flag set and a key that moved go; a key that is still valid stays, so a rotation
// with an overlap window disconnects nobody.
func TestStreams_RevalidateKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		routes      map[string]string
		name        string
		wantRevoked []string
	}{
		{
			name:        "the key is removed, another key of the flag set stays",
			routes:      map[string]string{keyOther: keyedSetA},
			wantRevoked: []string{connOld},
		},
		{
			name:        "the flag set is withdrawn",
			routes:      map[string]string{},
			wantRevoked: []string{connOld, connOther},
		},
		{
			name:        "a rotation with an overlap disconnects nobody",
			routes:      map[string]string{keyOld: keyedSetA, keyNew: keyedSetA, keyOther: keyedSetA},
			wantRevoked: nil,
		},
		{
			name:        "the rotation ends",
			routes:      map[string]string{keyNew: keyedSetA, keyOther: keyedSetA},
			wantRevoked: []string{connOld},
		},
		{
			name:        "the key moves to another flag set",
			routes:      map[string]string{keyOld: keyedSetB, keyOther: keyedSetA},
			wantRevoked: []string{connOld},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			websockets := stream.NewWebsocketService()
			streams := corehttp.NewStreamsWith(&corehttp.StreamsConfig{
				Websocket: websockets, SSE: stream.NewSSEService(), Label: t.Name(),
			})

			t.Cleanup(func() { _ = streams.Close(context.Background()) })

			conns := map[string]*closeSignal{
				connOld: newCloseSignal(), connOther: newCloseSignal(), connWithout: newCloseSignal(),
			}
			websockets.RegisterKeyed(conns[connOld], keyedSetA, keyOld)
			websockets.RegisterKeyed(conns[connOther], keyedSetA, keyOther)
			websockets.Register(conns[connWithout], keyedSetA)

			streams.Revalidate(keyedRuntime(tt.routes))

			assert.Equal(t, 3-len(tt.wantRevoked), streams.Connected().Websocket, "out of the list when it returns")

			for name, conn := range conns {
				revoked := false

				for _, want := range tt.wantRevoked {
					revoked = revoked || want == name
				}

				select {
				case <-conn.closed:
					assert.True(t, revoked, "%s was closed but is still valid", name)
				case <-time.After(waitForClose(revoked)):
					assert.False(t, revoked, "%s is revoked and was not closed", name)
				}
			}
		})
	}
}

// waitForClose is how long a test waits for a close: long for a connection that must be closed, short for one that must
// not (the answer is already settled by the revalidation).
func waitForClose(revoked bool) time.Duration {
	if revoked {
		return keyedBound
	}

	return 20 * time.Millisecond
}

// The swap does not wait for a connection that is slow to close: Revalidate returns with the client out of the list
// while its close is held, and the close runs after.
func TestStreams_RevalidateDoesNotWaitForTheClose(t *testing.T) {
	t.Parallel()

	websockets := stream.NewWebsocketService()
	streams := corehttp.NewStreamsWith(&corehttp.StreamsConfig{
		Websocket: websockets, SSE: stream.NewSSEService(), Label: t.Name(),
	})

	conn := newCloseSignal()
	conn.hold = make(chan struct{})

	// registered after the streams: the held close ends before the streams are joined
	t.Cleanup(func() {
		select {
		case <-conn.hold:
		default:
			close(conn.hold)
		}

		require.NoError(t, streams.Close(context.Background()))
	})

	websockets.RegisterKeyed(conn, keyedSetA, keyOld)

	done := make(chan struct{})

	go func() {
		streams.Revalidate(keyedRuntime(map[string]string{}))
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(keyedBound):
		require.FailNow(t, "Revalidate waited for the close of a connection")
	}

	assert.Zero(t, streams.Connected().Websocket, "the revoked client is out of the list")

	select {
	case <-conn.closed:
		assert.Fail(t, "the close was held and did not run")
	default:
	}

	close(conn.hold)

	select {
	case <-conn.closed:
	case <-time.After(keyedBound):
		assert.Fail(t, "the close did not run once it was released")
	}
}

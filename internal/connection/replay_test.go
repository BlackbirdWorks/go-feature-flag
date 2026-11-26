package connection_test

import (
	"bufio"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/connection"
)

// TestObserver_TransportReplayIsNotObserved measures what the observer counts. The server answers the first
// request, then reads the second request on the kept-alive connection and closes it without a single response
// byte. The Go transport replays an idempotent request in that case, on a new connection, and the SDK sees
// one attempt. So an observation is one SDK attempt, not one write on the wire: the wire can carry one more
// request, which the service never answered.
func TestObserver_TransportReplayIsNotObserved(t *testing.T) {
	t.Parallel()

	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	var (
		hits  atomic.Int32
		conns sync.WaitGroup
		mu    sync.Mutex
		open  []net.Conn
	)

	conns.Go(func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}

			mu.Lock()

			open = append(open, conn)
			mu.Unlock()

			conns.Go(func() {
				defer func() { _ = conn.Close() }()

				reader := bufio.NewReader(conn)

				for {
					req, readErr := http.ReadRequest(reader)
					if readErr != nil {
						return
					}

					if hits.Add(1) == 2 {
						return // the second request dies without a response byte
					}

					_, _ = conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n"))

					_ = req.Body.Close()
				}
			})
		}
	})

	t.Cleanup(func() {
		_ = listener.Close()

		mu.Lock()
		for _, conn := range open {
			_ = conn.Close()
		}
		mu.Unlock()

		conns.Wait()
	})

	rec := &recorder{}
	fac := connection.New(connection.WithObserver(rec))
	require.NoError(t, fac.Set(connC, other(func(spec *connection.S3Spec) {
		spec.Endpoint = "http://" + listener.Addr().String()
	})))

	client, err := fac.S3(t.Context(), connC)
	require.NoError(t, err)

	require.NoError(t, headObject(t, client))
	require.NoError(t, headObject(t, client))

	assert.Equal(t, []observation{{connC, opHead, resOK}, {connC, opHead, resOK}}, rec.all())
	assert.Equal(t, int32(3), hits.Load(), "2 SDK attempts, 3 requests received: the transport replayed one")
}

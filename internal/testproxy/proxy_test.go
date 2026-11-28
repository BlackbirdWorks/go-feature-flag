package testproxy_test

import (
	"bufio"
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/testproxy"
)

const ioBound = 5 * time.Second

// echoServer answers every line with the same line.
func echoServer(t *testing.T) string {
	t.Helper()

	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	t.Cleanup(func() { _ = listener.Close() })

	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}

			go func() {
				defer conn.Close()

				reader := bufio.NewReader(conn)

				for {
					line, readErr := reader.ReadString('\n')
					if readErr != nil {
						return
					}

					_, _ = conn.Write([]byte(line))
				}
			}()
		}
	}()

	return listener.Addr().String()
}

func startProxy(t *testing.T) *testproxy.Proxy {
	t.Helper()

	proxy, err := testproxy.Start(echoServer(t))
	require.NoError(t, err)
	t.Cleanup(proxy.Close)

	return proxy
}

func dial(t *testing.T, proxy *testproxy.Proxy) net.Conn {
	t.Helper()

	conn, err := (&net.Dialer{Timeout: ioBound}).DialContext(t.Context(), "tcp", proxy.Addr())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	require.NoError(t, conn.SetDeadline(time.Now().Add(ioBound)))

	return conn
}

func roundTrip(conn net.Conn, line string) (string, error) {
	_, err := conn.Write([]byte(line + "\n"))
	if err != nil {
		return "", err
	}

	got, err := bufio.NewReader(conn).ReadString('\n')

	return got, err
}

func TestProxy_ForwardsBothWays(t *testing.T) {
	t.Parallel()

	got, err := roundTrip(dial(t, startProxy(t)), "ping")
	require.NoError(t, err)
	assert.Equal(t, "ping\n", got)
}

func TestProxy_DelayHoldsEveryDirection(t *testing.T) {
	t.Parallel()

	const delay = 100 * time.Millisecond

	tests := []struct {
		name     string
		delay    time.Duration
		wantSlow bool
	}{
		{name: "no delay", delay: 0, wantSlow: false},
		{name: "a delay", delay: delay, wantSlow: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			proxy := startProxy(t)
			proxy.SetDelay(tt.delay)

			start := time.Now()
			got, err := roundTrip(dial(t, proxy), "ping")
			elapsed := time.Since(start)

			require.NoError(t, err)
			assert.Equal(t, "ping\n", got)
			// the request and the answer are each held once
			assert.Equal(t, tt.wantSlow, elapsed >= 2*delay, "elapsed %s", elapsed)
		})
	}
}

func TestProxy_CutClosesOpenConnectionsAndKeepsAccepting(t *testing.T) {
	t.Parallel()

	proxy := startProxy(t)
	conn := dial(t, proxy)

	_, err := roundTrip(conn, "one")
	require.NoError(t, err)

	proxy.Cut()

	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		_, cutErr := roundTrip(conn, "two")
		assert.Error(collect, cutErr, "the cut connection is closed")
	}, ioBound, 10*time.Millisecond, "the open connection was not closed by Cut")

	got, err := roundTrip(dial(t, proxy), "three")
	require.NoError(t, err)
	assert.Equal(t, "three\n", got, "a new connection works after a cut")
}

func TestProxy_RefuseClosesNewConnectionsUntilLifted(t *testing.T) {
	t.Parallel()

	proxy := startProxy(t)
	proxy.SetRefuse(true)

	_, err := roundTrip(dial(t, proxy), "refused")
	require.Error(t, err, "a refused connection is closed at once")

	proxy.SetRefuse(false)

	got, err := roundTrip(dial(t, proxy), "back")
	require.NoError(t, err)
	assert.Equal(t, "back\n", got)
}

func TestProxy_CloseIsIdempotentAndStopsTheListener(t *testing.T) {
	t.Parallel()

	proxy, err := testproxy.Start(echoServer(t))
	require.NoError(t, err)

	addr := proxy.Addr()

	proxy.Close()
	proxy.Close()

	_, err = (&net.Dialer{Timeout: time.Second}).DialContext(context.Background(), "tcp", addr)
	assert.Error(t, err, "nothing listens after Close")
}

func TestStart_FailsWhenTheTargetIsDown(t *testing.T) {
	t.Parallel()

	proxy, err := testproxy.Start("127.0.0.1:1")
	require.NoError(t, err, "the target is dialled per connection, not at the start")
	t.Cleanup(proxy.Close)

	_, err = roundTrip(dial(t, proxy), "x")
	assert.Error(t, err, "a connection to a dead target is closed")
}

func TestProxy_SetTargetRedirectsNewConnections(t *testing.T) {
	t.Parallel()

	proxy, err := testproxy.Start("127.0.0.1:1")
	require.NoError(t, err)
	t.Cleanup(proxy.Close)

	proxy.SetTarget(echoServer(t))

	got, err := roundTrip(dial(t, proxy), "moved")
	require.NoError(t, err)
	assert.Equal(t, "moved\n", got)
}

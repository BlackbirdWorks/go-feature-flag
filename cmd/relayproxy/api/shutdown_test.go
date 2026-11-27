package api_test

import (
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
)

// A shutdown that arrives before the server started returns at once without an error, and the start that follows
// returns at once too and closes the listener it was given.
func TestShutdownBeforeStart(t *testing.T) {
	t.Parallel()

	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	t.Cleanup(func() { _ = listener.Close() })

	conf := testConfig(t)
	conf.flags = newDefaultFlags(t, nil)
	srv := newTestServer(t, conf, &service.Services{}, slog.New(slog.DiscardHandler), api.WithListener(listener))

	require.NoError(t, srv.Shutdown(t.Context()))

	started := make(chan struct{})

	go func() {
		defer close(started)

		srv.StartWithContext(t.Context())
	}()

	select {
	case <-started:
	case <-time.After(10 * time.Second):
		require.FailNow(t, "the start after a shutdown did not return")
	}

	_, acceptErr := listener.Accept()
	assert.ErrorIs(t, acceptErr, net.ErrClosed, "the listener of the server was closed")
}

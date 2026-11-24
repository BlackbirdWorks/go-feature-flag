package testutils

import (
	"net"
	"testing"

	"github.com/stretchr/testify/require"
)

// ListenLocal binds a TCP listener on a free loopback port and returns it with the port. The listener stays
// bound until the test ends, so no other test can take the port: hand it to the component under test (for
// example api.WithListener) instead of closing it and binding the port again later. The component that
// serves on it closes it; the test cleanup closing it a second time is harmless.
func ListenLocal(t *testing.T) (net.Listener, int) {
	t.Helper()

	var lc net.ListenConfig
	listener, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	return listener, listener.Addr().(*net.TCPAddr).Port //nolint:errcheck // a tcp listener always has a *net.TCPAddr
}

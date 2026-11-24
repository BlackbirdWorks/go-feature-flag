package testutils

import (
	"net"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// GetFreePort returns a free port on the local machine.
func GetFreePort(t *testing.T) int {
	var lc net.ListenConfig
	listener, err := lc.Listen(t.Context(), "tcp", ":0") //nolint:gosec
	require.NoError(t, err)
	defer func() { _ = listener.Close() }()

	return listener.Addr().(*net.TCPAddr).Port //nolint:errcheck // a tcp listener always has a *net.TCPAddr
}

// GetFreePortAsString returns a free port on the local machine as a string.
func GetFreePortAsString(t *testing.T) string {
	port := GetFreePort(t)

	return strconv.Itoa(port)
}

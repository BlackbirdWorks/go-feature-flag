package relay_test

import (
	"net"
	"net/url"
	"testing"
	"time"

	"github.com/blackbirdworks/semaphore/internal/loadtest"
	"github.com/blackbirdworks/semaphore/internal/loadtest/relay"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// the relay of the soak must only be reachable through the loopback interface.
func TestStart_ListensOnLoopbackOnly(t *testing.T) {
	t.Parallel()
	addrs, err := net.InterfaceAddrs()
	require.NoError(t, err)
	var external []string
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil && !ipn.IP.IsLoopback() {
			external = append(external, ipn.IP.String())
		}
	}
	if len(external) == 0 {
		t.Skip("no non-loopback IPv4 address on this host: nothing to probe")
	}

	r, err := relay.Start(relay.Options{FlagsYAML: loadtest.FlagsYAML(), Listen: true})
	require.NoError(t, err)
	defer r.Close()
	u, err := url.Parse(r.BaseURL)
	require.NoError(t, err)

	loop, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", u.Port()), 5*time.Second)
	require.NoError(t, err, "reachable through the loopback interface")
	_ = loop.Close()
	for _, ip := range external {
		c, err := net.DialTimeout("tcp", net.JoinHostPort(ip, u.Port()), 5*time.Second)
		if err == nil {
			_ = c.Close()
		}
		assert.Error(t, err, "must not be reachable through %s", ip)
	}
}

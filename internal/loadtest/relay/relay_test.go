package relay_test

import (
	"context"
	"log/slog"
	"net"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/internal/loadtest"
	"github.com/blackbirdworks/semaphore/internal/loadtest/relay"
)

// the relay of the soak must only be reachable through the loopback interface.
// Observable outcome: the relay answers on 127.0.0.1 and refuses connections on every other local address.
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

	parsedURL, err := url.Parse(r.BaseURL)
	require.NoError(t, err)

	loop, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(context.Background(), "tcp",
		net.JoinHostPort("127.0.0.1", parsedURL.Port()))
	require.NoError(t, err, "reachable through the loopback interface")

	_ = loop.Close()

	for _, addr := range external {
		c, dialTimeoutErr := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(context.Background(), "tcp",
			net.JoinHostPort(addr, parsedURL.Port()))
		if dialTimeoutErr == nil {
			_ = c.Close()
		}

		assert.Error(t, dialTimeoutErr, "must not be reachable through %s", addr)
	}
}

// A relay that is started with a segments document serves a flag that uses its segment: the document is a manifest
// object that the flag set lists in its segmentSources.
func TestStartWithStore_ServesTheSegmentsOfTheDocument(t *testing.T) {
	t.Parallel()

	const flags = "seg-flag:\n  variations: {on: true, off: false}\n  targeting:\n" +
		"    - {name: vip, query: 'targetingKey in segment(\"vip\")', variation: on}\n" +
		"  defaultRule: {name: d, variation: off}\n"

	tests := []struct {
		name string
		key  string
		want bool
	}{
		{name: "a member", key: "alice", want: true},
		{name: "not a member", key: "bob", want: false},
	}

	r, err := relay.StartWithStore(&relay.Options{FlagsYAML: loadtest.FlagsYAML() + flags}, &relay.StoreOptions{
		SegmentsDoc: "apiVersion: semaphore/v1\nkind: Segments\nsegments:\n  - name: vip\n    members: [alice]\n",
		Logger:      slog.New(slog.DiscardHandler),
	})
	require.NoError(t, err)

	t.Cleanup(r.Close)

	set, err := r.FlagSets.FlagSet(relay.APIKey)
	require.NoError(t, err)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, evalErr := set.RawVariation(t.Context(), "seg-flag", ffcontext.NewEvaluationContext(tt.key), nil)
			require.NoError(t, evalErr)
			assert.Equal(t, tt.want, got.Value)
		})
	}
}

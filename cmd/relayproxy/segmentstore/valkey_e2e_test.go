//go:build docker

package segmentstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	testcontainerredis "github.com/testcontainers/testcontainers-go/modules/redis"
	"github.com/valkey-io/valkey-go"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/segmentstore"
	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

// A valkey connection is dialled by the loop, serves the lookups of a bound generation and the version of its key,
// and is closed by Shutdown.
func TestConnectionsValkeyServes(t *testing.T) {
	t.Parallel()

	container, err := testcontainerredis.Run(t.Context(), "docker.io/valkey/valkey:8-alpine")
	testcontainers.CleanupContainer(t, container)
	require.NoError(t, err)

	address, err := container.Endpoint(t.Context(), "")
	require.NoError(t, err)

	client, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{address}})
	require.NoError(t, err)
	t.Cleanup(client.Close)

	require.NoError(t, client.Do(t.Context(), client.B().Sadd().Key("aud").Member(memberAlice).Build()).Error())
	require.NoError(t, client.Do(t.Context(), client.B().Set().Key("aud-version").Value("7").Build()).Error())

	doc := "version: 2\nconnections:\n  store: {type: valkey, addresses: [\"" + address + "\"], forceSingleClient: true}\n"
	cfg, err := bootstrap.Parse([]byte(doc), nil, bootstrap.LoadOptions{})
	require.NoError(t, err)

	conns := segmentstore.NewConnections(cfg, segmentstore.Options{RetryInterval: tick})

	binding, err := conns.Bind("store", segment.MissingNotMember, []flagruntime.BoundDefinition{
		{Name: segAudience, Key: "aud", VersionKey: "aud-version", Layout: segment.LayoutSet},
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})

	go func() {
		defer close(done)

		conns.Run(ctx)
	}()

	require.Eventually(t, func() bool { return stateOf(conns, "store") == stateConnected }, waitFor, tick)

	lookups := []segment.Lookup{{Segment: segAudience, ID: memberAlice}, {Segment: segAudience, ID: "bob"}}
	require.NoError(t, binding.Provider.Contains(t.Context(), lookups))
	assert.Equal(
		t,
		[]segment.State{segment.Member, segment.NotMember},
		[]segment.State{lookups[0].State, lookups[1].State},
	)

	version, err := binding.Versioner.Version(t.Context(), segAudience)
	require.NoError(t, err)
	assert.Equal(t, "v:7", version)

	cancel()

	select {
	case <-done:
	case <-time.After(waitFor):
		assert.Fail(t, "the loop did not stop when its context ended")
	}

	require.NoError(t, conns.Shutdown(t.Context()))
	assert.Equal(t, "closed", stateOf(conns, "store"), "the client is gone after Shutdown")
}

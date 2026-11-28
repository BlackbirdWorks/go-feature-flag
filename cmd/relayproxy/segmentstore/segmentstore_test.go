package segmentstore_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/segmentstore"
	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

// New describes every connection of the file: its kind, and for a kind that serves segments the type that serves
// them, which implements the interfaces of the modes it can serve.
func TestNew(t *testing.T) {
	t.Parallel()

	const doc = `version: 2
connections:
  cache: {type: redis, addresses: ["127.0.0.1:6379"]}
  fast: {type: valkey, addresses: ["127.0.0.1:6380"]}
  control: {type: file, path: /tmp/control}
`

	cfg, err := bootstrap.Parse([]byte(doc), nil, bootstrap.LoadOptions{})
	require.NoError(t, err)

	table := segmentstore.New(cfg)
	require.Len(t, table, 3)

	tests := []struct {
		name         string
		conn         string
		wantKind     string
		wantLookup   bool
		wantSnapshot bool
		wantNoStore  bool
	}{
		{name: kindRedis, conn: connCache, wantKind: kindRedis, wantLookup: true, wantSnapshot: true},
		{name: "valkey", conn: connFast, wantKind: "valkey", wantLookup: true, wantSnapshot: true},
		{name: kindFile, conn: "control", wantKind: kindFile, wantNoStore: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			facts, found := table.SegmentStore(tt.conn)
			require.True(t, found)
			assert.Equal(t, tt.wantKind, facts.Kind)
			assert.Equal(t, tt.wantNoStore, facts.Store == nil)
			assert.Equal(t, tt.wantLookup, segment.SupportsMode(facts.Store, segment.ModeLookup))
			assert.Equal(t, tt.wantSnapshot, segment.SupportsMode(facts.Store, segment.ModeSnapshot))
		})
	}

	_, found := table.SegmentStore("nowhere")
	assert.False(t, found, "a name that the file does not have")
}

// A file without connections gives an empty table.
func TestNew_NoConnections(t *testing.T) {
	t.Parallel()

	assert.Empty(t, segmentstore.New(&bootstrap.Config{}))
}

package segmentstore

import (
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/modules/core/segment/redissegment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/valkeysegment"
)

// storeOf is the type that serves the segments of a kind of connection, as a typed nil, or nil for a kind that cannot
// serve segments.
func storeOf(kind string) any {
	switch kind {
	case bootstrap.TypeRedis:
		return (*redissegment.Provider)(nil)
	case bootstrap.TypeValkey:
		return (*valkeysegment.Provider)(nil)
	}

	return nil
}

// New describes the connections of a bootstrap configuration: every connection by name, with its kind and, for a kind
// that serves segments, the type that serves it. A configuration without connections gives an empty table.
func New(cfg *bootstrap.Config) flagruntime.StoreTable {
	table := make(flagruntime.StoreTable, len(cfg.Connections))

	for name := range cfg.Connections {
		conn := cfg.Connections[name]
		kind := conn.Type()
		table[name] = flagruntime.SegmentStore{Kind: kind, Store: storeOf(kind)}
	}

	return table
}

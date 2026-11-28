package relaycore

import (
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/segmentstore"
	"github.com/blackbirdworks/semaphore/modules/core/segment/resolver"
)

// SegmentsStatus is the state of the dynamic segments of the process.
type SegmentsStatus struct {
	// Sets is the live health of the served dynamic segments of each flag set that has some.
	Sets map[string]SegmentSetHealth `json:"sets,omitempty"`
	// Stores is the state of each connection that can serve segments.
	Stores []segmentstore.ConnectionState `json:"stores,omitempty"`
	// Bindings is the number of shared bindings that the served runtime uses (one for each segments object, connection
	// and reading of a missing key), and the state of the held sets of each connection.
	Bindings flagruntime.SegmentBindingsStatus `json:"bindings"`
	// Resolver sums what the resolvers of every generation did since the start.
	Resolver resolver.Stats `json:"resolver"`
}

// SegmentSetHealth is the number of served dynamic segments of a flag set that are healthy and that have a store
// error.
type SegmentSetHealth struct {
	Healthy    int `json:"healthy"`
	StoreError int `json:"storeError"`
}

// segmentsStatus reads the live state: the connections, the views of the runtime that is served and the counters.
// It evaluates nothing and calls no store.
func (c *Core) segmentsStatus() SegmentsStatus {
	rt := c.holder.Load()
	out := SegmentsStatus{Stores: c.segs.States(), Resolver: c.segObs.Stats(), Bindings: rt.SegmentBindings()}

	for name, set := range rt.FlagSets {
		if set.Segments == nil || set.Segments.View == nil {
			continue
		}

		if out.Sets == nil {
			out.Sets = map[string]SegmentSetHealth{}
		}

		healthy, failing := set.Segments.View.Health()
		out.Sets[name] = SegmentSetHealth{Healthy: healthy, StoreError: failing}
	}

	return out
}

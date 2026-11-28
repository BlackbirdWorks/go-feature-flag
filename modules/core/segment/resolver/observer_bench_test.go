package resolver_test

import (
	"testing"
	"time"

	"github.com/blackbirdworks/semaphore/modules/core/segment/resolver"
)

// BenchmarkCountersObserve is what the observer costs per call of the resolver and per segment: atomics, no allocation.
func BenchmarkCountersObserve(b *testing.B) {
	counters := &resolver.Counters{}
	call := resolver.CallObservation{Lookups: 2, Hits: 1, Misses: 1, Unknown: 1, Duration: time.Millisecond}
	seg := resolver.SegmentObservation{
		Segment: "audience",
		Lookups: 2,
		Hits:    1,
		Misses:  1,
		Unknown: 1,
		Failure: resolver.ReasonStore,
	}

	b.ReportAllocs()

	for b.Loop() {
		counters.ObserveCall(call)
		counters.ObserveSegment(seg)
	}
}

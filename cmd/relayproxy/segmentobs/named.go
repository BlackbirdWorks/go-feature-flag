package segmentobs

import (
	"maps"
	"sync"
	"sync/atomic"

	"github.com/blackbirdworks/semaphore/modules/core/segment/resolver"
)

// The answers of a segment that the per-segment series tell apart.
const (
	namedHit = iota
	namedMiss
	namedCoalesced
	namedUnknown
	namedCount
)

// namedSeries are the counts of one segment name.
type namedSeries struct {
	results [namedCount]atomic.Uint64
	reasons [resolver.ReasonCount]atomic.Uint64
}

// named holds the series of the segment names, up to a cap; the other names share the series of [OtherSegment].
type named struct {
	other    *namedSeries
	series   map[string]*namedSeries
	overflow atomic.Uint64
	limit    int
	mu       sync.RWMutex
}

func newNamed(limit int) *named {
	return &named{limit: limit, other: &namedSeries{}, series: map[string]*namedSeries{}}
}

// observe counts the part of a call that concerns one segment. A nil receiver (the series are off) does nothing.
func (n *named) observe(obs *resolver.SegmentObservation) {
	if n == nil {
		return
	}

	series := n.seriesOf(obs.Segment)
	series.results[namedHit].Add(uint64(max(obs.Hits, 0)))
	series.results[namedMiss].Add(uint64(max(obs.Misses, 0)))
	series.results[namedCoalesced].Add(uint64(max(obs.Coalesced, 0)))
	series.results[namedUnknown].Add(uint64(max(obs.Unknown, 0)))

	if obs.Unknown > 0 {
		reason := int(obs.Failure)
		if reason < 0 || reason >= resolver.ReasonCount {
			reason = int(resolver.ReasonNone)
		}

		series.reasons[reason].Add(uint64(obs.Unknown))
	}
}

func (n *named) seriesOf(name string) *namedSeries {
	if name == OtherSegment {
		return n.other
	}

	n.mu.RLock()
	found := n.series[name]
	n.mu.RUnlock()

	if found != nil {
		return found
	}

	n.mu.Lock()
	defer n.mu.Unlock()

	found = n.series[name]
	if found != nil {
		return found
	}

	if len(n.series) >= n.limit {
		n.overflow.Add(1)

		return n.other
	}

	found = &namedSeries{}
	n.series[name] = found

	return found
}

// snapshot copies the map of the series; the series themselves are read with atomics.
func (n *named) snapshot() map[string]*namedSeries {
	n.mu.RLock()
	defer n.mu.RUnlock()

	out := make(map[string]*namedSeries, len(n.series)+1)
	maps.Copy(out, n.series)
	out[OtherSegment] = n.other

	return out
}

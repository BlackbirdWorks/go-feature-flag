package segmentobs

import (
	"maps"
	"slices"
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

// named holds the series of the live segment names, up to a cap; the other names share the series of
// [OtherSegment]. The set of names is replaced as a whole when a generation is applied ([named.setLive]) and read
// without a lock: an observation is one atomic load, one map read and atomic adds.
type named struct {
	other    *namedSeries
	live     atomic.Pointer[map[string]*namedSeries]
	overflow atomic.Uint64
	limit    int
	mu       sync.Mutex
}

func newNamed(limit int) *named {
	n := &named{limit: limit, other: &namedSeries{}}
	empty := map[string]*namedSeries{}
	n.live.Store(&empty)

	return n
}

// setLive makes the names of the live segments the names that have a series: the first cap of them in sorted order
// (so which names get one does not depend on which was seen first). A name that stays keeps its series and its counts;
// a name that is gone loses its series and frees its slot.
func (n *named) setLive(names []string) {
	if n == nil {
		return
	}

	n.mu.Lock()
	defer n.mu.Unlock()

	sorted := slices.Sorted(slices.Values(names))
	sorted = slices.Compact(sorted)
	sorted = slices.DeleteFunc(sorted, func(name string) bool { return name == OtherSegment })

	if len(sorted) > n.limit {
		sorted = sorted[:n.limit]
	}

	previous := *n.live.Load()
	next := make(map[string]*namedSeries, len(sorted))

	for _, name := range sorted {
		if series, ok := previous[name]; ok {
			next[name] = series

			continue
		}

		next[name] = &namedSeries{}
	}

	n.live.Store(&next)
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

// seriesOf takes no lock: a name that has no series (beyond the cap, or not live) counts under other.
func (n *named) seriesOf(name string) *namedSeries {
	if found, ok := (*n.live.Load())[name]; ok {
		return found
	}

	if name != OtherSegment {
		n.overflow.Add(1)
	}

	return n.other
}

// snapshot returns the series of the live names and the other bucket.
func (n *named) snapshot() map[string]*namedSeries {
	live := *n.live.Load()
	out := make(map[string]*namedSeries, len(live)+1)
	maps.Copy(out, live)
	out[OtherSegment] = n.other

	return out
}

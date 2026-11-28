package segmentobs

import (
	"slices"
	"strings"
	"sync"

	"github.com/blackbirdworks/semaphore/modules/core/segment/resolver"
)

const (
	// DefaultMaxSegments is the cap of distinct segment names of the per-segment series.
	DefaultMaxSegments = 100
	// OtherSegment is the value of the segment label for the names beyond the cap.
	OtherSegment = "other"
)

// Options configures the observers.
type Options struct {
	// PerSegment turns on the series labelled by segment name.
	PerSegment bool
	// MaxSegments caps the distinct segment names of the per-segment series. Zero means [DefaultMaxSegments].
	MaxSegments int
}

// Observers is the source of the observers of the resolvers: one for each connection. It is also the Prometheus
// collector of what they count.
type Observers struct {
	named *named
	conns map[string]*ConnObserver
	mu    sync.Mutex
}

// New returns observers with the options.
func New(opts Options) *Observers {
	obs := &Observers{conns: map[string]*ConnObserver{}}

	if opts.PerSegment {
		limit := opts.MaxSegments
		if limit <= 0 {
			limit = DefaultMaxSegments
		}

		obs.named = newNamed(limit)
	}

	return obs
}

// ConnObserver counts the calls of the resolvers of one connection. The embedded counters count with atomics only,
// so a call takes no lock and allocates nothing; the segments are added to the per-segment series when they are on.
type ConnObserver struct {
	*resolver.Counters

	named *named
}

// ObserveSegment implements [resolver.Observer].
func (c *ConnObserver) ObserveSegment(obs resolver.SegmentObservation) {
	c.Counters.ObserveSegment(obs)
	c.named.observe(&obs)
}

// For returns the observer of the resolvers of a connection. The same connection gets the same observer. It is meant
// for the build of a binding, never for the evaluation path.
func (o *Observers) For(connection string) *ConnObserver {
	o.mu.Lock()
	defer o.mu.Unlock()

	conn := o.conns[connection]
	if conn == nil {
		conn = &ConnObserver{Counters: &resolver.Counters{}, named: o.named}
		o.conns[connection] = conn
	}

	return conn
}

// Stats sums what the resolvers of every connection did since the start.
func (o *Observers) Stats() resolver.Stats {
	var out resolver.Stats

	for _, conn := range o.snapshot() {
		total := &conn.stats
		out.Calls += total.Calls
		out.Lookups += total.Lookups
		out.Hits += total.Hits
		out.Misses += total.Misses
		out.Coalesced += total.Coalesced
		out.ShortCircuited += total.ShortCircuited
		out.NotLoaded += total.NotLoaded
		out.Oversized += total.Oversized
		out.Invalid += total.Invalid
		out.Unknown += total.Unknown
		out.Failed += total.Failed
		out.Duration += total.Duration
		out.BreakerOpened += total.BreakerOpened
		out.BreakerHalfOpened += total.BreakerHalfOpened
		out.BreakerClosed += total.BreakerClosed
	}

	return out
}

// reading is what one connection counted.
type reading struct {
	name  string
	stats resolver.Stats
	dist  resolver.Distribution
}

// snapshot reads the counters of each connection, sorted by name.
func (o *Observers) snapshot() []*reading {
	o.mu.Lock()
	defer o.mu.Unlock()

	out := make([]*reading, 0, len(o.conns))
	for name, conn := range o.conns {
		out = append(out, &reading{name: name, stats: conn.Snapshot(), dist: conn.Distribution()})
	}

	slices.SortFunc(out, func(a, b *reading) int { return strings.Compare(a.name, b.name) })

	return out
}

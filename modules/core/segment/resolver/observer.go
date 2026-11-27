package resolver

import (
	"sync/atomic"
	"time"
)

// CallObservation describes one call of Contains. It holds counts only, never an id.
type CallObservation struct {
	// Lookups is the number of lookups of the call.
	Lookups int
	// Hits is the number answered by the cache.
	Hits int
	// Misses is the number this call asked the store for.
	Misses int
	// Coalesced is the number that joined a call of another caller.
	Coalesced int
	// ShortCircuited is the number dropped by an open breaker.
	ShortCircuited int
	// Unknown is the number left Unknown.
	Unknown int
	// Duration is the time of the call, read from the clock package.
	Duration time.Duration
	// Failed is true when the call returned an error.
	Failed bool
}

// BreakerEvent is a change of state of the breaker of a segment.
type BreakerEvent struct {
	Segment  string
	From, To BreakerState
}

// Observer receives what the resolver does. Its methods are called on the goroutine of the caller or of the shared
// call, so they must be fast and safe for concurrent use.
type Observer interface {
	ObserveCall(CallObservation)
	ObserveBreaker(BreakerEvent)
}

// Stats is a snapshot of the counters of [Counters].
type Stats struct {
	// Calls .. Failed sum the fields of the observations of the same name.
	Calls, Lookups, Hits, Misses, Coalesced, ShortCircuited, Unknown, Failed uint64
	// Duration is the sum of the durations.
	Duration time.Duration
	// BreakerOpened, BreakerHalfOpened and BreakerClosed count the transitions to each state.
	BreakerOpened, BreakerHalfOpened, BreakerClosed uint64
}

// Counters is an [Observer] that counts with atomics. The zero value is ready to use.
type Counters struct {
	calls, lookups, hits, misses, coalesced, shorted, unknown, failed atomic.Uint64
	opened, halfOpened, closed                                        atomic.Uint64
	duration                                                          atomic.Int64
}

// ObserveCall implements [Observer].
func (c *Counters) ObserveCall(obs CallObservation) {
	c.calls.Add(1)
	c.lookups.Add(uint64(max(obs.Lookups, 0)))
	c.hits.Add(uint64(max(obs.Hits, 0)))
	c.misses.Add(uint64(max(obs.Misses, 0)))
	c.coalesced.Add(uint64(max(obs.Coalesced, 0)))
	c.shorted.Add(uint64(max(obs.ShortCircuited, 0)))
	c.unknown.Add(uint64(max(obs.Unknown, 0)))
	c.duration.Add(int64(max(obs.Duration, 0)))

	if obs.Failed {
		c.failed.Add(1)
	}
}

// ObserveBreaker implements [Observer].
func (c *Counters) ObserveBreaker(ev BreakerEvent) {
	switch ev.To {
	case BreakerOpen:
		c.opened.Add(1)
	case BreakerHalfOpen:
		c.halfOpened.Add(1)
	case BreakerClosed:
		c.closed.Add(1)
	}
}

// Snapshot returns the current counts.
func (c *Counters) Snapshot() Stats {
	return Stats{
		Calls: c.calls.Load(), Lookups: c.lookups.Load(), Hits: c.hits.Load(), Misses: c.misses.Load(),
		Coalesced: c.coalesced.Load(), ShortCircuited: c.shorted.Load(), Unknown: c.unknown.Load(),
		Failed: c.failed.Load(), Duration: time.Duration(c.duration.Load()),
		BreakerOpened: c.opened.Load(), BreakerHalfOpened: c.halfOpened.Load(), BreakerClosed: c.closed.Load(),
	}
}

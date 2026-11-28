package resolver

import (
	"sync/atomic"
	"time"
)

// Reason is why the lookups of a segment in a call stayed Unknown, for the observers. The first reason wins.
type Reason uint8

const (
	// ReasonNone means every lookup was answered.
	ReasonNone Reason = iota
	// ReasonStore means the call to the store failed or left a lookup unanswered.
	ReasonStore
	// ReasonCircuitOpen means the breaker of the segment dropped the lookups.
	ReasonCircuitOpen
	// ReasonWaiting means the context of the caller, or the shared call, ended while it waited.
	ReasonWaiting
	// ReasonNotLoaded means the store said recently that the segment is not loaded, so it was not asked again.
	ReasonNotLoaded
	// ReasonOversized means the id is longer than MaxIDBytes.
	ReasonOversized
	// ReasonInvalid means the id is not valid UTF-8 and the segment normalises its ids.
	ReasonInvalid
	// ReasonUnanswered means the store replied without error and without an answer for the lookup.
	ReasonUnanswered
	// ReasonOverdue means too many calls to the store were still running after their timeout.
	ReasonOverdue
)

// String implements [fmt.Stringer].
func (r Reason) String() string {
	switch r {
	case ReasonNone:
		return "none"
	case ReasonStore:
		return "store"
	case ReasonCircuitOpen:
		return "circuit_open"
	case ReasonWaiting:
		return "waiting"
	case ReasonNotLoaded:
		return "not_loaded"
	case ReasonOversized:
		return "oversized"
	case ReasonInvalid:
		return "invalid"
	case ReasonOverdue:
		return "overdue"
	case ReasonUnanswered:
		return "unanswered"
	}

	return "invalid_reason"
}

// CallObservation describes one call of Contains. It holds counts only, never an id.
type CallObservation struct {
	// Lookups is the number of lookups of the call.
	Lookups int
	// Hits is the number answered by the cache.
	Hits int
	// Misses is the number this call asked the store for.
	Misses int
	// Coalesced is the number of lookups that joined a flight that already existed: one of another caller, or an earlier
	// lookup of the same call for the same id. Each of them costs no call to the store.
	Coalesced int
	// ShortCircuited is the number of lookups that were not sent to the store because the breaker of their segment was
	// open, or because too many calls were overdue (the Failure of the segment tells which: ReasonCircuitOpen or
	// ReasonOverdue).
	ShortCircuited int
	// NotLoaded is the number not asked because the segment was recently found not loaded.
	NotLoaded int
	// Oversized is the number left Unknown because the id is longer than MaxIDBytes.
	Oversized int
	// Invalid is the number left Unknown because the id is not valid UTF-8 and is normalised.
	Invalid int
	// Unknown is the number left Unknown.
	Unknown int
	// Duration is the time of the call, read from the clock package.
	Duration time.Duration
	// Failed is true when the call returned an error.
	Failed bool
}

// SegmentObservation is the part of a call that concerns one segment. A call that touches more than
// MaxObservedSegments segments reports the first ones.
type SegmentObservation struct {
	// Segment is the name of the segment.
	Segment string
	// Lookups is the number of lookups of the segment in the call.
	Lookups int
	// Hits, Misses, Coalesced and Unknown are as in [CallObservation], for this segment.
	Hits, Misses, Coalesced, Unknown int
	// Failure is why lookups stayed Unknown.
	Failure Reason
}

// MaxObservedSegments is the number of segments of one call that are reported one by one.
const MaxObservedSegments = 8

// BreakerEvent is a change of state of the breaker of a segment.
type BreakerEvent struct {
	Segment  string
	From, To BreakerState
}

// Observer receives what the resolver does. Its methods are called on the goroutine of the caller or of the shared
// call, so they must be fast, safe for concurrent use and must not call the resolver.
type Observer interface {
	ObserveCall(CallObservation)
	ObserveSegment(SegmentObservation)
	ObserveBreaker(BreakerEvent)
}

// Stats is a snapshot of the counters of [Counters].
type Stats struct {
	// Calls .. Failed sum the fields of the observations of the same name.
	Calls, Lookups, Hits, Misses, Coalesced, ShortCircuited, NotLoaded, Oversized, Invalid, Unknown, Failed uint64
	// Duration is the sum of the durations.
	Duration time.Duration
	// BreakerOpened, BreakerHalfOpened and BreakerClosed count the transitions to each state.
	BreakerOpened, BreakerHalfOpened, BreakerClosed uint64
}

// Counters is an [Observer] that counts with atomics. The zero value is ready to use.
type Counters struct {
	calls, lookups, hits, misses, coalesced, shorted, notLoaded, oversized, invalid, unknown, failed atomic.Uint64
	opened, halfOpened, closed                                                                       atomic.Uint64
	duration                                                                                         atomic.Int64
	dist                                                                                             distribution
}

// ObserveCall implements [Observer].
//
//nolint:gocritic // hugeParam: the observation is passed by value so that it does not escape to the heap
func (c *Counters) ObserveCall(obs CallObservation) {
	c.calls.Add(1)
	c.lookups.Add(uint64(max(obs.Lookups, 0)))
	c.hits.Add(uint64(max(obs.Hits, 0)))
	c.misses.Add(uint64(max(obs.Misses, 0)))
	c.coalesced.Add(uint64(max(obs.Coalesced, 0)))
	c.shorted.Add(uint64(max(obs.ShortCircuited, 0)))
	c.notLoaded.Add(uint64(max(obs.NotLoaded, 0)))
	c.oversized.Add(uint64(max(obs.Oversized, 0)))
	c.invalid.Add(uint64(max(obs.Invalid, 0)))
	c.unknown.Add(uint64(max(obs.Unknown, 0)))
	c.duration.Add(int64(max(obs.Duration, 0)))
	c.dist.buckets[durationBucket(max(obs.Duration, 0))].Add(1)

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
		Coalesced: c.coalesced.Load(), ShortCircuited: c.shorted.Load(), NotLoaded: c.notLoaded.Load(),
		Oversized: c.oversized.Load(), Invalid: c.invalid.Load(), Unknown: c.unknown.Load(),
		Failed: c.failed.Load(), Duration: time.Duration(c.duration.Load()),
		BreakerOpened: c.opened.Load(), BreakerHalfOpened: c.halfOpened.Load(), BreakerClosed: c.closed.Load(),
	}
}

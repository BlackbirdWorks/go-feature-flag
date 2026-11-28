package resolver

import (
	"sync/atomic"
	"time"
)

// ReasonCount is the number of values of [Reason].
const ReasonCount = int(ReasonOverdue) + 1

// DurationBucketCount is the number of buckets of the duration of a call: one for each of [DurationBounds] and one for
// the calls above the last bound.
const DurationBucketCount = 14

// boundsArray are the upper bounds of the buckets, from a cache hit and a local store to a call that reaches its
// timeout.
func boundsArray() [DurationBucketCount - 1]time.Duration {
	return [DurationBucketCount - 1]time.Duration{
		500 * time.Microsecond, time.Millisecond, 2500 * time.Microsecond, 5 * time.Millisecond, 10 * time.Millisecond,
		25 * time.Millisecond, 50 * time.Millisecond, 100 * time.Millisecond, 250 * time.Millisecond,
		500 * time.Millisecond, time.Second, 2500 * time.Millisecond, 5 * time.Second,
	}
}

// DurationBounds returns the inclusive upper bounds of the duration buckets of [Distribution]; the last has none.
func DurationBounds() []time.Duration {
	b := boundsArray()

	return b[:]
}

func durationBucket(d time.Duration) int {
	bounds := boundsArray()
	i := 0

	for i < len(bounds) && d > bounds[i] {
		i++
	}

	return i
}

// Distribution is what [Counters] knows beyond the totals of [Stats].
type Distribution struct {
	// DurationBuckets counts the calls by duration, not cumulative (see [DurationBounds]).
	DurationBuckets [DurationBucketCount]uint64
	// Reasons counts the lookups that stayed Unknown by [Reason] of their segment (up to MaxObservedSegments segments of
	// a call are reported).
	Reasons [ReasonCount]uint64
}

// distribution holds the atomics of [Distribution].
type distribution struct {
	buckets [DurationBucketCount]atomic.Uint64
	reasons [ReasonCount]atomic.Uint64
}

// ObserveSegment implements [Observer]. The counters keep the totals of the calls, and the unknown lookups by reason.
func (c *Counters) ObserveSegment(obs SegmentObservation) {
	if obs.Unknown <= 0 {
		return
	}

	reason := int(obs.Failure)
	if reason < 0 || reason >= ReasonCount {
		reason = int(ReasonNone)
	}

	c.dist.reasons[reason].Add(uint64(obs.Unknown))
}

// Distribution returns the current duration buckets and reasons.
func (c *Counters) Distribution() Distribution {
	var out Distribution

	for i := range c.dist.buckets {
		out.DurationBuckets[i] = c.dist.buckets[i].Load()
	}

	for i := range c.dist.reasons {
		out.Reasons[i] = c.dist.reasons[i].Load()
	}

	return out
}

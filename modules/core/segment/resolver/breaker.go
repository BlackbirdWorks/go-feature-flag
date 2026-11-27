package resolver

import (
	"sync"
	"time"
)

// BreakerState is the state of the circuit breaker of a segment.
type BreakerState uint8

const (
	// BreakerClosed lets every call through.
	BreakerClosed BreakerState = iota
	// BreakerOpen answers Unknown at once, without a call to the store.
	BreakerOpen
	// BreakerHalfOpen lets one probe call through; its result closes or reopens the breaker.
	BreakerHalfOpen
)

// String implements [fmt.Stringer].
func (s BreakerState) String() string {
	switch s {
	case BreakerClosed:
		return "closed"
	case BreakerOpen:
		return "open"
	case BreakerHalfOpen:
		return "half-open"
	}

	return "invalid"
}

// outcome is how a call to the store went for a segment.
type outcome uint8

const (
	// outcomeSuccess means the store answered.
	outcomeSuccess outcome = iota
	// outcomeFailure means the call failed and left a lookup of the segment unanswered.
	outcomeFailure
	// outcomeNeutral means the store replied without error and could not tell (a set that is not loaded): it says
	// nothing about the health of the store.
	outcomeNeutral
)

// transition is a change of state, reported to the observer.
type transition struct {
	from, to BreakerState
	changed  bool
}

// breaker counts consecutive failed calls. It is safe for concurrent use. Every change of state starts a new
// generation: a call carries the generation it was allowed in, and a result of an older generation (a call that
// started before the breaker opened and finishes while it is half-open) is ignored.
type breaker struct {
	openedAt  time.Time
	mu        sync.Mutex
	threshold int
	failures  int
	cooldown  time.Duration
	gen       uint64
	state     BreakerState
	probing   bool
}

func newBreaker(opts SegmentOptions) *breaker {
	return &breaker{threshold: opts.BreakerFailures, cooldown: opts.BreakerCooldown}
}

// allow reports whether a call may go to the store at the instant now, and the generation to report the result
// with. An open breaker whose cool-down has passed lets exactly one call through, the probe.
func (b *breaker) allow(now time.Time) (bool, uint64, transition) {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case BreakerClosed:
		return true, b.gen, transition{}
	case BreakerOpen:
		if now.Sub(b.openedAt) < b.cooldown {
			return false, b.gen, transition{}
		}

		b.state, b.probing = BreakerHalfOpen, true
		b.gen++

		return true, b.gen, transition{from: BreakerOpen, to: BreakerHalfOpen, changed: true}
	case BreakerHalfOpen:
		if b.probing {
			return false, b.gen, transition{}
		}

		b.probing = true
		b.gen++

		return true, b.gen, transition{}
	}

	return false, b.gen, transition{}
}

// record reports the result of a call that allow let through, with the generation that allow returned.
func (b *breaker) record(gen uint64, res outcome, now time.Time) transition {
	b.mu.Lock()
	defer b.mu.Unlock()

	if gen != b.gen {
		return transition{} // a call of an older generation: its result says nothing about the present
	}

	switch b.state {
	case BreakerClosed:
		return b.recordClosed(res, now)
	case BreakerHalfOpen:
		return b.recordProbe(res, now)
	case BreakerOpen:
		// nothing is allowed while open: a result of this generation cannot exist
	}

	return transition{}
}

func (b *breaker) recordClosed(res outcome, now time.Time) transition {
	switch res {
	case outcomeSuccess:
		b.failures = 0
	case outcomeNeutral:
		// not a failure and not a proof of health
	case outcomeFailure:
		b.failures++
		if b.failures >= b.threshold {
			b.state, b.openedAt = BreakerOpen, now
			b.gen++

			return transition{from: BreakerClosed, to: BreakerOpen, changed: true}
		}
	}

	return transition{}
}

func (b *breaker) recordProbe(res outcome, now time.Time) transition {
	switch res {
	case outcomeSuccess:
		b.state, b.failures, b.probing = BreakerClosed, 0, false
		b.gen++

		return transition{from: BreakerHalfOpen, to: BreakerClosed, changed: true}
	case outcomeFailure:
		b.state, b.openedAt, b.probing = BreakerOpen, now, false
		b.gen++

		return transition{from: BreakerHalfOpen, to: BreakerOpen, changed: true}
	case outcomeNeutral:
		// the probe learned nothing: the next call may probe again
		b.probing = false
		b.gen++
	}

	return transition{}
}

func (b *breaker) snapshot() (BreakerState, int) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.state, b.failures
}

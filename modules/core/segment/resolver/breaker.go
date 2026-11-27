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

// transition is a change of state, reported to the observer.
type transition struct {
	from, to BreakerState
	changed  bool
}

// breaker counts consecutive failed calls. It is safe for concurrent use.
type breaker struct {
	openedAt  time.Time
	mu        sync.Mutex
	threshold int
	failures  int
	cooldown  time.Duration
	state     BreakerState
}

func newBreaker(opts SegmentOptions) *breaker {
	return &breaker{threshold: opts.BreakerFailures, cooldown: opts.BreakerCooldown}
}

// allow reports whether a call may go to the store at the instant now. An open breaker whose cool-down has passed
// lets exactly one call through, the probe.
func (b *breaker) allow(now time.Time) (bool, transition) {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case BreakerClosed:
		return true, transition{}
	case BreakerOpen:
		if now.Sub(b.openedAt) < b.cooldown {
			return false, transition{}
		}

		b.state = BreakerHalfOpen

		return true, transition{from: BreakerOpen, to: BreakerHalfOpen, changed: true}
	case BreakerHalfOpen:
		return false, transition{}
	}

	return false, transition{}
}

// record reports the result of a call that allow let through.
func (b *breaker) record(success bool, now time.Time) transition {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case BreakerClosed:
		if success {
			b.failures = 0

			return transition{}
		}

		b.failures++
		if b.failures < b.threshold {
			return transition{}
		}

		b.state, b.openedAt = BreakerOpen, now

		return transition{from: BreakerClosed, to: BreakerOpen, changed: true}
	case BreakerHalfOpen:
		if success {
			b.state, b.failures = BreakerClosed, 0

			return transition{from: BreakerHalfOpen, to: BreakerClosed, changed: true}
		}

		b.state, b.openedAt = BreakerOpen, now

		return transition{from: BreakerHalfOpen, to: BreakerOpen, changed: true}
	case BreakerOpen:
		// a call that started before the breaker opened: its result changes nothing
	}

	return transition{}
}

func (b *breaker) snapshot() (BreakerState, int) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.state, b.failures
}

package valkeysegment

import (
	"sync/atomic"
	"time"
)

// Observation describes one call of Contains. It holds counts only, never an id or a key.
type Observation struct {
	// Lookups is the number of lookups of the call.
	Lookups int
	// Answered is the number of lookups set to Member or NotMember.
	Answered int
	// Oversized is the number of lookups left Unknown because the id is longer than MaxIDBytes.
	Oversized int
	// Unknown is the number of lookups left Unknown (a failure, an unconfigured segment, a missing key).
	Unknown int
	// RoundTrips is 1 when a pipeline was sent and 0 when nothing was sent.
	RoundTrips int
	// Duration is the time of the call, read from the clock package.
	Duration time.Duration
	// Failure is the class of the failure, ClassNone when the call returned no error.
	Failure Class
}

// Observer receives one Observation per call. It is called on the goroutine of the caller, so it must be fast and
// safe for concurrent use. The relay proxy wires its metrics through it.
type Observer interface {
	Observe(Observation)
}

// Stats is a snapshot of the counters of [Counters].
type Stats struct {
	Failures map[string]uint64
	Calls    uint64
	Lookups  uint64
	Answered uint64
	Unknown  uint64
	// Oversized is the number of lookups left Unknown because the id was too long.
	Oversized  uint64
	RoundTrips uint64
	Duration   time.Duration
}

// Counters is an [Observer] that counts with atomics. The zero value is ready to use.
type Counters struct {
	calls, lookups, answered, unknown, oversized, roundTrips atomic.Uint64
	duration                                                 atomic.Int64
	failures                                                 [numClasses]atomic.Uint64
}

// Observe implements [Observer].
func (c *Counters) Observe(obs Observation) {
	c.calls.Add(1)
	c.lookups.Add(uint64(max(obs.Lookups, 0)))
	c.answered.Add(uint64(max(obs.Answered, 0)))
	c.unknown.Add(uint64(max(obs.Unknown, 0)))
	c.oversized.Add(uint64(max(obs.Oversized, 0)))
	c.roundTrips.Add(uint64(max(obs.RoundTrips, 0)))
	c.duration.Add(int64(max(obs.Duration, 0)))

	if obs.Failure > ClassNone && obs.Failure < numClasses {
		c.failures[obs.Failure].Add(1)
	}
}

// Snapshot returns the current counts.
func (c *Counters) Snapshot() Stats {
	stats := Stats{
		Calls:      c.calls.Load(),
		Lookups:    c.lookups.Load(),
		Answered:   c.answered.Load(),
		Unknown:    c.unknown.Load(),
		Oversized:  c.oversized.Load(),
		RoundTrips: c.roundTrips.Load(),
		Duration:   time.Duration(c.duration.Load()),
		Failures:   make(map[string]uint64),
	}

	for _, class := range Classes() {
		if n := c.failures[class].Load(); n > 0 {
			stats.Failures[class.String()] = n
		}
	}

	return stats
}

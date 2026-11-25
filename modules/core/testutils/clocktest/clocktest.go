// Package clocktest pins clock.Now for the tests of a package.
//
// A test package whose code under test reads the clock calls Freeze once, from its TestMain,
// before m.Run, and builds its fixtures relative to Instant. Tests never reassign clock.Now and
// never restore it: a test that needs another instant passes it explicitly (evaluation date,
// currentDateTime, ...).
package clocktest

import (
	"sync/atomic"
	"time"

	"github.com/blackbirdworks/semaphore/modules/core/clock"
)

// Instant is the fixed instant that Freeze pins the clock to.
func Instant() time.Time {
	return time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
}

// Freeze pins clock.Now to Instant. Call it from TestMain only, before m.Run.
func Freeze() {
	instant := Instant()
	//nolint:reassign // the one place that overrides the clock, from the TestMain of the packages that need it.
	clock.Now = func() time.Time { return instant }
}

// FreezeStepping pins clock.Now to a deterministic stepping clock: the first call returns Instant, and
// every following call returns a time that is step later than the previous one (strictly increasing
// and safe for concurrent use, with no relation to the real time). It is for the packages whose tests
// assert that a date advances (a cache refresh after a poll or a forced refresh). Call it from TestMain
// only, before m.Run, and never reassign the clock afterwards.
func FreezeStepping(step time.Duration) {
	var calls atomic.Int64

	instant := Instant()
	//nolint:reassign // the one place that overrides the clock, from the TestMain of the packages that need it.
	clock.Now = func() time.Time {
		return instant.Add(time.Duration(calls.Add(1)-1) * step)
	}
}

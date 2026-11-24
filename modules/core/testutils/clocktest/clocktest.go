// Package clocktest pins clock.Now for the tests of a package.
//
// A test package whose code under test reads the clock calls Freeze once, from its TestMain,
// before m.Run, and builds its fixtures relative to Instant. Tests never reassign clock.Now and
// never restore it: a test that needs another instant passes it explicitly (evaluation date,
// currentDateTime, ...).
package clocktest

import (
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

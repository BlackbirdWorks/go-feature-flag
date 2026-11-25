// Package clock is the one place that reads the wall clock for the data that semaphore stamps,
// compares or evaluates against (evaluation dates, event creation dates, cache refresh dates,
// rate-limit windows, token lifetimes, log timestamps).
//
// It is a leaf package so that every package can import it without a cycle.
package clock

import "time"

// Now returns the current time. It is [time.Now] unless a test package replaced it in its TestMain
// (see the clocktest package): production code must not assign it.
//
// It is the only package-level variable that semaphore allows, on purpose: the alternative is to
// thread a clock through every constructor, evaluation and log call for the benefit of tests.
// Measured durations (time.Since, deadlines of network connections, retry back-offs) do not go
// through it: they need the real clock.
//
//nolint:gochecknoglobals // single allowed global so tests can swap wall-clock time via clocktest
var Now = time.Now

package flagdef_test

import (
	"time"

	"github.com/blackbirdworks/semaphore/ffcontext"
)

// fixedNow is the instant the wall-clock fixtures of these tests are built on. The
// code under test reads the evaluation date from the evaluation context (currentDateTime),
// so a test pins the clock by evaluating at this instant (see atFixedNow) instead of
// racing the real clock.
func fixedNow() time.Time {
	return time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
}

// atFixedNow returns a copy of ctx whose evaluation date is fixedNow, unless ctx already
// carries one. The given context is left untouched (test cases run in parallel).
func atFixedNow(ctx ffcontext.Context) ffcontext.EvaluationContext {
	specifics := ctx.ExtractGOFFProtectedFields()
	if specifics.CurrentDateTime == nil {
		now := fixedNow()
		specifics.CurrentDateTime = &now
	}

	out := ffcontext.NewEvaluationContext(ctx.GetKey())
	for k, v := range ctx.GetCustom() {
		if k == ffcontext.ReservedKey || k == ffcontext.LegacyReservedKey {
			continue
		}

		out.AddCustomAttribute(k, v)
	}

	out.AddCustomAttribute(ffcontext.ReservedKey, specifics)

	return out
}

// atDate returns a copy of ctx whose evaluation date is date, replacing the one it carries. The
// given context is left untouched (test cases run in parallel).
func atDate(ctx ffcontext.Context, date time.Time) ffcontext.EvaluationContext {
	specifics := ctx.ExtractGOFFProtectedFields()
	specifics.CurrentDateTime = &date

	out := ffcontext.NewEvaluationContext(ctx.GetKey())
	for k, v := range ctx.GetCustom() {
		if k == ffcontext.ReservedKey || k == ffcontext.LegacyReservedKey {
			continue
		}

		out.AddCustomAttribute(k, v)
	}

	out.AddCustomAttribute(ffcontext.ReservedKey, specifics)

	return out
}

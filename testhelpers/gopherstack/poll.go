package gopherstack

import (
	"context"
	"fmt"
	"time"
)

// PollReport is what Poll saw, for the failure message of a test that polls the emulator.
type PollReport struct {
	// LastErr is the last error a step returned, nil when it never did.
	LastErr error
	// Polls is the number of steps that ran.
	Polls int
	// Incomplete is the number of steps that ran without error and did not finish the job.
	Incomplete int
	// Done reports that a step finished the job.
	Done bool
}

// String says what happened, for a failure message built after the polling ended.
func (r PollReport) String() string {
	return fmt.Sprintf("after %d polls (%d without error that did not finish), last error: %v",
		r.Polls, r.Incomplete, r.LastErr)
}

// Poll runs step until it reports done or ctx ends. The deadline of the whole wait is the one of ctx: every call of
// step gets its own context, cut at perCall and never later than the deadline of ctx, so that a call that hangs is
// stopped and nothing runs past the deadline. Between two steps it waits interval (or until ctx ends). It makes no
// read of the clock. A step reports an error for a call that failed; Poll records it and polls again.
func Poll(
	ctx context.Context, perCall, interval time.Duration, step func(ctx context.Context) (bool, error),
) PollReport {
	var rep PollReport

	for {
		callCtx, cancel := context.WithTimeout(ctx, perCall)
		done, err := step(callCtx)

		cancel()

		rep.Polls++

		switch {
		case done:
			rep.Done = true

			return rep
		case err != nil:
			rep.LastErr = err
		default:
			rep.Incomplete++
		}

		timer := time.NewTimer(interval)

		select {
		case <-ctx.Done():
			timer.Stop()

			return rep
		case <-timer.C:
		}
	}
}

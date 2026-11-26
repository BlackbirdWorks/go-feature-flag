package gopherstack

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// maxDistinctErrors bounds the list of distinct errors of a PollReport; the others are only counted.
const maxDistinctErrors = 10

// OpError is an error of a call of the emulator, with the operation that failed and how many times it did.
type OpError struct {
	// Err is the first error with this text.
	Err error
	// Op is the name of the operation, as the caller gave it.
	Op string
	// Count is the number of times the same operation failed with the same text.
	Count int
}

// PollReport is what Poll saw, for the failure message of a test that polls the emulator.
type PollReport struct {
	// Errors lists the distinct errors (operation and text) in the order they first happened, at most
	// maxDistinctErrors of them.
	Errors []OpError
	// Polls is the number of steps that ran.
	Polls int
	// Incomplete is the number of steps that made no failed call and did not finish the job.
	Incomplete int
	// Dropped counts the errors that were not listed because the list was full.
	Dropped int
	// Done reports that a step finished the job.
	Done bool
}

// String says what happened, for a failure message built after the polling ended.
func (r *PollReport) String() string {
	var b strings.Builder

	fmt.Fprintf(&b, "after %d polls (%d answered without finishing)", r.Polls, r.Incomplete)

	if len(r.Errors) == 0 {
		b.WriteString(", no error")

		return b.String()
	}

	b.WriteString(", errors:")

	for i := range r.Errors {
		fmt.Fprintf(&b, " [%s x%d: %v]", r.Errors[i].Op, r.Errors[i].Count, r.Errors[i].Err)
	}

	if r.Dropped > 0 {
		fmt.Fprintf(&b, " and %d more not listed", r.Dropped)
	}

	return b.String()
}

func (r *PollReport) record(name string, err error) {
	for i := range r.Errors {
		if r.Errors[i].Op == name && r.Errors[i].Err.Error() == err.Error() {
			r.Errors[i].Count++

			return
		}
	}

	if len(r.Errors) == maxDistinctErrors {
		r.Dropped++

		return
	}

	r.Errors = append(r.Errors, OpError{Op: name, Err: err, Count: 1})
}

// Calls makes the calls of one step. Each call gets its own context, cut at the per-call bound and never later than
// the deadline of the whole wait, so a call that is slow does not take the time of the next one, and nothing runs past
// the deadline. A step cannot share one context across calls by mistake: it has no other way to call.
type Calls struct {
	ctx      context.Context //nolint:containedctx // the deadline of the whole wait, for the calls of one step
	rep      *PollReport
	perCall  time.Duration
	interval time.Duration
	failed   int
}

// Do runs fn with its own call context. An error is recorded in the report under name (the operation) and returned.
func (c *Calls) Do(name string, fn func(ctx context.Context) error) error {
	ctx, cancel := context.WithTimeout(c.ctx, c.perCall)
	defer cancel()

	err := fn(ctx)
	if err != nil {
		c.rep.record(name, err)
		c.failed++
	}

	return err
}

// Retry is Do again and again, with the polling interval between two attempts, until fn succeeds or the deadline of the
// whole wait passes. It returns nil on success, else the error of the last attempt. Every failed attempt is in the
// report.
func (c *Calls) Retry(name string, fn func(ctx context.Context) error) error {
	for {
		err := c.Do(name, fn)
		if err == nil {
			return nil
		}

		timer := time.NewTimer(c.interval)

		select {
		case <-c.ctx.Done():
			timer.Stop()

			return err
		case <-timer.C:
		}
	}
}

// Within returns a context for one call that is made outside a step (a setup call): cut at perCall and never later
// than the deadline of ctx.
func Within(ctx context.Context, perCall time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, perCall)
}

// Poll runs step until it returns true or ctx ends. The deadline of the whole wait is the one of ctx. The step makes
// its calls through the Calls it is given. Between two steps Poll waits interval (or until ctx ends). It makes no read
// of the clock.
func Poll(ctx context.Context, perCall, interval time.Duration, step func(c *Calls) bool) *PollReport {
	rep := &PollReport{}

	for {
		calls := &Calls{ctx: ctx, rep: rep, perCall: perCall, interval: interval}
		done := step(calls)

		rep.Polls++

		switch {
		case done:
			rep.Done = true

			return rep
		case calls.failed == 0:
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

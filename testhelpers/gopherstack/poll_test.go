package gopherstack_test

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/testhelpers/gopherstack"
)

var (
	errBoom  = errors.New(textBoom)
	errOther = errors.New("other")
)

const (
	overall  = time.Second
	perCall  = 300 * time.Millisecond
	interval = 100 * time.Millisecond
	textBoom = "boom"
	opRecv   = "Receive"
	opDelete = "Delete"
)

// work makes a call that takes d, unless its context ends first.
func work(d time.Duration) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		select {
		case <-time.After(d):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func fail(err error) func(context.Context) error { return func(context.Context) error { return err } }

type wantError struct {
	op       string
	text     string
	minCount int
}

func TestPollReportsWhatItSawAndGivesEveryCallItsOwnBound(t *testing.T) {
	t.Parallel()

	tests := []struct {
		step              func(t *testing.T, call int, c *gopherstack.Calls) bool
		name              string
		wantSummary       string
		wantErrors        []wantError
		wantMinPolls      int
		wantExactPolls    int
		wantDropped       int
		deadline          time.Duration
		wantDone          bool
		wantStopped       bool
		wantAllIncomplete bool
	}{
		{
			name: "every call fails with the same error",
			step: func(_ *testing.T, _ int, c *gopherstack.Calls) bool {
				_ = c.Do(opRecv, fail(errBoom))

				return false
			},
			wantErrors:   []wantError{{op: opRecv, text: textBoom, minCount: 5}},
			wantMinPolls: 5,
			wantSummary:  "[Receive x",
		},
		{
			name:              "every call answers and the job is never finished",
			step:              func(*testing.T, int, *gopherstack.Calls) bool { return false },
			wantSummary:       "no error",
			wantMinPolls:      5,
			wantAllIncomplete: true,
		},
		{
			name: "every call hangs until its context ends",
			step: func(_ *testing.T, _ int, c *gopherstack.Calls) bool {
				_ = c.Do(opRecv, func(ctx context.Context) error {
					<-ctx.Done()

					return ctx.Err()
				})

				return false
			},
			wantErrors:   []wantError{{op: opRecv, text: "context deadline exceeded", minCount: 3}},
			wantMinPolls: 3,
		},
		{
			name: "the job is finished on the third call",
			step: func(_ *testing.T, call int, _ *gopherstack.Calls) bool {
				return call == 3
			},
			wantDone:       true,
			wantExactPolls: 3,
			wantSummary:    "after 3 polls (2 answered without finishing), no error",
		},
		{
			// 250 ms each, the bound of a call is 300 ms: with one context shared by the three the second would fail.
			name: "three calls of one step each get a full bound",
			step: func(t *testing.T, call int, c *gopherstack.Calls) bool {
				t.Helper()

				for range 3 {
					require.NoError(t, c.Do(opRecv, work(250*time.Millisecond)))
				}

				return call == 1
			},
			wantDone:       true,
			wantExactPolls: 1,
			wantSummary:    "no error",
		},
		{
			// the call would take an hour: it is cut at its bound (300 ms) and the polling ends at the overall deadline.
			name: "a call is cut at the overall deadline, not at its own bound",
			step: func(_ *testing.T, _ int, c *gopherstack.Calls) bool {
				_ = c.Do(opRecv, work(time.Hour))

				return false
			},
			wantErrors:   []wantError{{op: opRecv, text: "context deadline exceeded", minCount: 1}},
			wantMinPolls: 1,
		},
		{
			name: "different errors are kept with their operation and count",
			step: func(_ *testing.T, _ int, c *gopherstack.Calls) bool {
				_ = c.Do(opRecv, fail(errBoom))
				_ = c.Do(opDelete, fail(errOther))
				_ = c.Do(opRecv, fail(errOther))

				return false
			},
			wantErrors: []wantError{
				{op: opRecv, text: textBoom, minCount: 3},
				{op: opDelete, text: "other", minCount: 3},
				{op: opRecv, text: "other", minCount: 3},
			},
			wantMinPolls: 3,
		},
		{
			// 15 polls at 100 ms apart take 1.4 s of this row's 10 s deadline: it is never close to it, and the
			// 15th poll ends the job, so the counts are exact. 15 distinct errors: 10 listed, 5 only counted.
			name: "the list of distinct errors is bounded",
			step: func(_ *testing.T, call int, c *gopherstack.Calls) bool {
				_ = c.Do(opRecv, fail(fmt.Errorf("%w %d", errOther, call)))

				return call == 15
			},
			deadline:       10 * time.Second,
			wantDone:       true,
			wantExactPolls: 15,
			wantDropped:    5,
		},
		{
			name: "a failing delete ends the polling after that step, with the delete error",
			step: func(t *testing.T, _ int, calls *gopherstack.Calls) bool {
				t.Helper()

				require.NoError(t, calls.Do(opRecv, work(time.Millisecond)))

				err := calls.Do(opDelete, fail(errBoom))
				require.ErrorIs(t, err, errBoom, "Do returns the error of the delete")

				calls.Stop()

				return false
			},
			wantStopped:    true,
			wantExactPolls: 1,
			wantErrors:     []wantError{{op: opDelete, text: textBoom, minCount: 1}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				deadline := cmp.Or(tt.deadline, overall)

				outer, cancel := context.WithTimeout(t.Context(), deadline)
				defer cancel()

				calls := 0

				rep := gopherstack.Poll(outer, perCall, interval, func(c *gopherstack.Calls) bool {
					calls++

					return tt.step(t, calls, c)
				})

				assert.Equal(t, tt.wantDone, rep.Done)
				assert.Equal(t, calls, rep.Polls)
				assert.GreaterOrEqual(t, rep.Polls, tt.wantMinPolls)
				assert.Equal(t, tt.wantDropped, rep.Dropped)
				assert.Contains(t, rep.String(), tt.wantSummary)

				if tt.wantExactPolls > 0 {
					assert.Equal(t, tt.wantExactPolls, rep.Polls)
				}

				if tt.wantAllIncomplete {
					assert.Equal(t, rep.Polls, rep.Incomplete)
				}

				require.Len(t, rep.Errors, max(len(tt.wantErrors), boundedLen(tt.wantDropped)))

				for i, want := range tt.wantErrors {
					assert.Equal(t, want.op, rep.Errors[i].Op)
					assert.Contains(t, rep.Errors[i].Err.Error(), want.text)
					assert.GreaterOrEqual(t, rep.Errors[i].Count, want.minCount)
				}

				if !tt.wantDone && !tt.wantStopped {
					require.Error(t, outer.Err(), "it returned because the deadline passed")
				}
			})
		})
	}
}

// boundedLen is the length of a full list of distinct errors when some were dropped, else 0.
func boundedLen(dropped int) int {
	if dropped > 0 {
		return 10
	}

	return 0
}

// A call never ends after the deadline of the whole wait, whatever its own bound is.
func TestNoCallOutlivesTheOverallDeadline(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		outer, cancel := context.WithTimeout(t.Context(), overall)
		defer cancel()

		outerDeadline, _ := outer.Deadline()

		rep := gopherstack.Poll(outer, time.Hour, interval, func(c *gopherstack.Calls) bool {
			_ = c.Do(opRecv, func(ctx context.Context) error {
				deadline, ok := ctx.Deadline()
				require.True(t, ok)
				assert.False(t, deadline.After(outerDeadline), "the call is cut at the overall deadline")

				<-ctx.Done()

				return ctx.Err()
			})

			return false
		})

		assert.False(t, rep.Done)
		require.Error(t, outer.Err())
	})
}

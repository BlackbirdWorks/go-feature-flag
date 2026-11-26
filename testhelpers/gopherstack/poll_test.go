package gopherstack_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/testhelpers/gopherstack"
)

var errBoom = errors.New("boom")

const (
	overall  = time.Second
	perCall  = 300 * time.Millisecond
	interval = 100 * time.Millisecond
)

func TestPollReportsWhatItSawWhenTheDeadlinePasses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		step                func(call int, ctx context.Context) (bool, error)
		wantLastErrIs       error
		name                string
		wantInSummary       string
		wantAtLeastNPolls   int
		wantExactlyNPolls   int
		wantDone            bool
		wantNoError         bool
		wantAllIncomplete   bool
		wantLastErrDeadline bool
	}{
		{
			name:              "every call fails",
			step:              func(int, context.Context) (bool, error) { return false, errBoom },
			wantInSummary:     "last error: boom",
			wantAtLeastNPolls: 5,
			wantLastErrIs:     errBoom,
		},
		{
			name:              "every call answers and the job is never finished",
			step:              func(int, context.Context) (bool, error) { return false, nil },
			wantInSummary:     "last error: <nil>",
			wantAtLeastNPolls: 5,
			wantNoError:       true,
			wantAllIncomplete: true,
		},
		{
			name: "every call hangs until its context ends",
			step: func(_ int, ctx context.Context) (bool, error) {
				<-ctx.Done()

				return false, ctx.Err()
			},
			wantInSummary:       "context deadline exceeded",
			wantAtLeastNPolls:   3,
			wantLastErrDeadline: true,
		},
		{
			name: "the job is finished on the third call",
			step: func(call int, _ context.Context) (bool, error) {
				return call == 3, nil
			},
			wantDone:          true,
			wantExactlyNPolls: 3,
			wantNoError:       true,
			wantInSummary:     "after 3 polls (2 without error that did not finish)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				outer, cancel := context.WithTimeout(t.Context(), overall)
				defer cancel()

				outerDeadline, _ := outer.Deadline()
				calls := 0

				rep := gopherstack.Poll(outer, perCall, interval, func(ctx context.Context) (bool, error) {
					calls++

					deadline, ok := ctx.Deadline()
					require.True(t, ok)
					assert.False(t, deadline.After(outerDeadline), "no call outlives the overall deadline")

					return tt.step(calls, ctx)
				})

				assert.Equal(t, tt.wantDone, rep.Done)
				assert.Equal(t, calls, rep.Polls)
				assert.Contains(t, rep.String(), tt.wantInSummary)

				if tt.wantExactlyNPolls > 0 {
					assert.Equal(t, tt.wantExactlyNPolls, rep.Polls)
				}

				assert.GreaterOrEqual(t, rep.Polls, tt.wantAtLeastNPolls)

				if tt.wantNoError {
					require.NoError(t, rep.LastErr)
				}

				if tt.wantLastErrIs != nil {
					require.ErrorIs(t, rep.LastErr, tt.wantLastErrIs)
				}

				if tt.wantLastErrDeadline {
					require.ErrorIs(t, rep.LastErr, context.DeadlineExceeded)
				}

				if tt.wantAllIncomplete {
					assert.Equal(t, rep.Polls, rep.Incomplete)
				}

				if !tt.wantDone {
					require.Error(t, outer.Err(), "it returned because the deadline passed")
				}
			})
		})
	}
}

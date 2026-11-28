package loadtest_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/loadtest"
)

var errEvalFailed = errors.New("eval failed")

// failEvery is a target whose every n-th request fails.
type failEvery struct{ n int }

func (f failEvery) Eval(_, i int) error {
	if f.n > 0 && i%f.n == 0 {
		return errEvalFailed
	}

	return nil
}

func (failEvery) Extra() map[string]int64 { return nil }
func (failEvery) Close()                  {}

func TestRunPacedRejectsOptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts loadtest.PacedOptions
	}{
		{name: "no rate", opts: loadtest.PacedOptions{Duration: time.Second, Workers: 1}},
		{name: "no duration", opts: loadtest.PacedOptions{Rate: 10, Workers: 1}},
		{name: "no workers", opts: loadtest.PacedOptions{Rate: 10, Duration: time.Second}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := loadtest.RunPaced(t.Context(), failEvery{}, tt.opts, nil)
			require.ErrorIs(t, err, loadtest.ErrPacedOptions)
		})
	}
}

func TestRunPacedCountsRequestsAndWindows(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		failEvery    int
		disturbances int
		wantFailures int
		wantWindows  int
	}{
		{name: "no failure, no window", wantFailures: 0, wantWindows: 0},
		{name: "a window and failures", failEvery: 100, disturbances: 1, wantFailures: 5, wantWindows: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			const rate, workers = 500, 4

			report, err := loadtest.RunPaced(t.Context(), failEvery{n: tt.failEvery}, loadtest.PacedOptions{
				Rate: rate, Duration: time.Second, Workers: workers, Tail: 10 * time.Millisecond,
			}, func(ctx context.Context, windows *loadtest.Windows) error {
				for range tt.disturbances {
					windows.Begin()
					windows.End()
				}

				<-ctx.Done()

				return ctx.Err()
			})
			require.NoError(t, err)

			assert.Equal(t, rate, report.Inside.Requests+report.Outside.Requests)
			assert.Equal(t, tt.wantFailures, report.Inside.Failures+report.Outside.Failures)
			assert.Equal(t, tt.wantWindows, report.Windows)
			assert.Positive(t, report.Outside.P99)
			assert.GreaterOrEqual(t, report.Outside.P99, report.Outside.P50)
		})
	}
}

func TestRunPacedReportsTheDisturbanceError(t *testing.T) {
	t.Parallel()

	_, err := loadtest.RunPaced(t.Context(), failEvery{}, loadtest.PacedOptions{
		Rate: 100, Duration: 200 * time.Millisecond, Workers: 1,
	}, func(context.Context, *loadtest.Windows) error { return errEvalFailed })

	require.ErrorIs(t, err, errEvalFailed)
}

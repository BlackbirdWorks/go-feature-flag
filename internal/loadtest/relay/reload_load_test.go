//go:build bench

package relay_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/internal/loadtest"
	"github.com/blackbirdworks/semaphore/internal/loadtest/relay"
)

const (
	// durationEnv selects the length of the run: the release check runs 60 s (task test-reload-load), the default is
	// the short variant that CI runs.
	durationEnv     = "SEMAPHORE_RELOAD_LOAD_DURATION"
	defaultDuration = 10 * time.Second

	loadRate    = 1000
	loadWorkers = 8
	reloadEvery = 200 * time.Millisecond
	windowTail  = 25 * time.Millisecond
	markerFlag  = "reload-marker"
	reloadBound = 30 * time.Second
)

// markedFlags is the flag file of the load test with one more flag whose value is n: every reload changes the file.
func markedFlags(n int) string {
	return fmt.Sprintf("%s%s:\n  variations:\n    A: %d\n  defaultRule:\n    variation: A\n",
		loadtest.FlagsYAML(), markerFlag, n)
}

// TestReloadLoad_P99IsFlatDuringReloads sends a constant request rate to a relay proxy (file store, real HTTP server)
// for the length of the run while a reload that changes the flag file runs every reloadEvery. It asserts that no
// request fails, that every reload was applied, and that the p99 of the requests due while a reload is in progress
// (from its start to its end plus windowTail) is flat, see assertFlat.
//
// It measures wall time and the process shares the machine with nothing else: the parallel tests of the package are
// paused until it ends, so it does not run in parallel.
//
//nolint:paralleltest // latency is the measure: a parallel test would load the machine during the run
func TestReloadLoad_P99IsFlatDuringReloads(t *testing.T) {
	duration := defaultDuration

	if raw, ok := os.LookupEnv(durationEnv); ok {
		parsed, err := time.ParseDuration(raw)
		require.NoError(t, err, durationEnv)

		duration = parsed
	}

	flagKey := loadtest.FlagKey(loadtest.Scenarios()[0], loadtest.Types()[0], false)

	target, err := relay.NewTarget(relay.Options{FlagsYAML: markedFlags(0)}, relay.SemaphoreEval, flagKey, loadWorkers)
	require.NoError(t, err)

	t.Cleanup(target.Close)

	var reloads int

	disturb := func(ctx context.Context, windows *loadtest.Windows) error {
		ticker := time.NewTicker(reloadEvery)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
			}

			// a reload in flight when the run ends is waited for, so the last one counted is the one served
			reloadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), reloadBound)

			windows.Begin()

			reloadErr := target.Relay().Republish(reloadCtx, markedFlags(reloads+1))

			cancel()

			if reloadErr != nil {
				return fmt.Errorf("reload %d: %w", reloads+1, reloadErr)
			}

			windows.End()

			reloads++
		}
	}

	report, err := loadtest.RunPaced(t.Context(), target, loadtest.PacedOptions{
		Rate: loadRate, Duration: duration, Workers: loadWorkers, Tail: windowTail,
	}, disturb)
	require.NoError(t, err)

	t.Logf("run %s, %d req/s: %d reloads (windows cover %s)", report.Elapsed.Round(time.Millisecond), loadRate,
		report.Windows, report.WindowTime.Round(time.Millisecond))
	t.Logf("inside reload windows: %+v", report.Inside)
	t.Logf("outside reload windows: %+v", report.Outside)
	t.Logf("p99 inside / outside = %.2f", float64(report.Inside.P99)/float64(report.Outside.P99))

	assert.Equal(t, int(duration.Seconds())*loadRate, report.Inside.Requests+report.Outside.Requests,
		"every request was sent")
	assert.Zero(t, report.Inside.Failures+report.Outside.Failures, "no request failed")
	assert.Equal(t, reloads, report.Windows)
	assert.GreaterOrEqual(t, reloads, int(duration/reloadEvery)/2, "the reloads kept coming")
	assert.Positive(t, report.Inside.Requests, "requests were due during reloads")

	set, err := target.Relay().FlagSets.FlagSet(relay.APIKey)
	require.NoError(t, err)

	got, err := set.RawVariation(markerFlag, ffcontext.NewEvaluationContext("check"), nil)
	require.NoError(t, err)
	assert.EqualValues(t, reloads, got.Value, "the last reload is the one served")

	assertFlat(t, &report)
}

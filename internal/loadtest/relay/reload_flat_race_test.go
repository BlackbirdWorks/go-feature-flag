//go:build bench && race

package relay_test

import (
	"testing"

	"github.com/blackbirdworks/semaphore/internal/loadtest"
)

// assertFlat only logs the figures under the race detector: the detector slows the rebuild of the flags ten times, the
// windows then cover half of the run and the p99 during them measures the detector (up to 5.7 times the p99 outside
// on a loaded machine). See reload_flat_test.go for the check of the normal build.
func assertFlat(t *testing.T, report *loadtest.PacedReport) {
	t.Helper()
	t.Logf("race build: p99 inside %s, outside %s, not asserted", report.Inside.P99, report.Outside.P99)
}

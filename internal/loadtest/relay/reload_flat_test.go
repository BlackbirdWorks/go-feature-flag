//go:build bench && !race

package relay_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/internal/loadtest"
)

const (
	// flatFactor is how many times the p99 outside the reloads the p99 during them may be. Measured on an idle
	// machine the two are equal within 3% (1.00 to 1.03 in 20 runs); the factor is the headroom for a machine that
	// other processes share (2.15 was seen next to a parallel race build).
	flatFactor = 3
	// flatSlack is the other allowance: a p99 during the reloads that is within this of the p99 outside is flat, whatever
	// the ratio (on a loaded machine the p99 outside is a few milliseconds and a ratio of it is a small number).
	flatSlack = 10 * time.Millisecond
)

// assertFlat checks that the p99 of the requests due during a reload is at most flatFactor times the p99 of the others,
// or at most flatSlack above it. It is built without the race detector only: the detector slows the rebuild of the
// flags ten times and the windows then cover half of the run, so the figure measures the detector, not the relay (the
// race build still checks that no request fails and that every reload applied).
func assertFlat(t *testing.T, report *loadtest.PacedReport) {
	t.Helper()

	allowed := max(flatFactor*report.Outside.P99, report.Outside.P99+flatSlack)

	assert.LessOrEqual(t, report.Inside.P99, allowed, "p99 during the reloads against p99 outside them")
}

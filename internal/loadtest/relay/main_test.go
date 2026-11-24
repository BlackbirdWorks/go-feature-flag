package relay_test

import (
	"os"
	"testing"

	"github.com/blackbirdworks/semaphore/modules/core/testutils/clocktest"
)

// TestMain pins the clock once for the whole package: the events that the harness produces are stamped
// with clock.Now. The durations that the harness measures use the real clock, which is not pinned.
func TestMain(m *testing.M) {
	clocktest.Freeze()
	os.Exit(m.Run())
}

package flagruntime_test

import (
	"testing"

	"github.com/blackbirdworks/semaphore/modules/core/testhelpers/clocktest"
)

// TestMain pins the clock once for the whole package: evaluations read clock.Now, and the time dependent flags of
// the tests (scheduled, progressive, experimentation) are checked against clocktest.Instant.
func TestMain(m *testing.M) {
	clocktest.Freeze()
	m.Run()
}

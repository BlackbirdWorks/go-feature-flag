package ffclient_test

import (
	"os"
	"testing"
	"time"

	"github.com/blackbirdworks/semaphore/modules/core/testutils/clocktest"
)

// TestMain pins the clock once for the whole package to a stepping clock: the tests assert that the
// cache refresh date advances after a poll or a forced refresh, which a frozen clock could not show and
// the real clock would make depend on the scheduler.
func TestMain(m *testing.M) {
	clocktest.FreezeStepping(time.Microsecond)
	os.Exit(m.Run())
}

package semaphore_test

import (
	"testing"
	"time"

	"github.com/blackbirdworks/semaphore/modules/core/testhelpers/clocktest"
)

// TestMain pins the clock once for the whole package to a stepping clock (the SSE event ids tell the broadcasts
// apart and the refresh date must advance): the product code under test stamps events and
// dates with clock.Now, and the fixtures are built relative to clocktest.Instant.
func TestMain(m *testing.M) {
	clocktest.FreezeStepping(time.Microsecond)
	m.Run()
}

package flag_test

import (
	"testing"

	"github.com/blackbirdworks/semaphore/modules/core/testutils/clocktest"
)

// TestMain pins the clock once for the whole package: the code under test stamps and compares
// against clock.Now, and the fixtures are built relative to clocktest.Instant.
func TestMain(m *testing.M) {
	clocktest.Freeze()
	m.Run()
}

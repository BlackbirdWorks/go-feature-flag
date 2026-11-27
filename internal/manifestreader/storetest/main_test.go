package storetest_test

import (
	"testing"

	"github.com/blackbirdworks/semaphore/modules/core/testhelpers/clocktest"
)

// TestMain freezes the clock of the package: the toy store that reads a deadline compares it with the clock, and
// the deadlines of the suite are fixed instants far from any clock.
func TestMain(m *testing.M) {
	clocktest.Freeze()

	m.Run()
}

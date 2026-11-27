package corehttp_test

import (
	"testing"

	"github.com/blackbirdworks/semaphore/modules/core/testhelpers/clocktest"
)

// TestMain pins the clock once for the whole package: the scheduled flags and the dates are relative to it.
func TestMain(m *testing.M) {
	clocktest.Freeze()
	m.Run()
}

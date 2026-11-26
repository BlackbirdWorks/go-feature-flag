//go:build docker

package events_test

import (
	"os"
	"testing"

	"github.com/blackbirdworks/semaphore/modules/core/testhelpers/clocktest"
	"github.com/blackbirdworks/semaphore/testhelpers/gopherstack"
)

// TestMain starts the gopherstack emulator shared by the tests of this package, and removes it at the end.
func TestMain(m *testing.M) {
	clocktest.FreezeStepping(clockStep)
	os.Exit(gopherstack.Main(m))
}

//go:build docker

package publish_test

import (
	"os"
	"testing"

	"github.com/blackbirdworks/semaphore/modules/core/testhelpers/clocktest"
	"github.com/blackbirdworks/semaphore/testhelpers/gopherstack"
)

// TestMain pins the clock and starts the gopherstack emulator shared by the e2e tests of this package.
func TestMain(m *testing.M) {
	clocktest.Freeze()
	os.Exit(gopherstack.Main(m))
}

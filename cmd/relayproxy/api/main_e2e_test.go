//go:build docker

package api_test

import (
	"os"
	"testing"

	"github.com/blackbirdworks/semaphore/modules/core/testutils/clocktest"
	"github.com/blackbirdworks/semaphore/testutils/gopherstack"
)

// TestMain starts the gopherstack emulator shared by the tests of this package, and removes it at the end.
func TestMain(m *testing.M) {
	clocktest.Freeze()
	os.Exit(gopherstack.Main(m))
}

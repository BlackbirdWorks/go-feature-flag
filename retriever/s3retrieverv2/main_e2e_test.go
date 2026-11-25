//go:build docker

package s3retrieverv2_test

import (
	"os"
	"testing"

	"github.com/blackbirdworks/semaphore/testhelpers/gopherstack"
)

// TestMain starts the gopherstack emulator shared by the tests of this package, and removes it at the end.
func TestMain(m *testing.M) {
	os.Exit(gopherstack.Main(m))
}

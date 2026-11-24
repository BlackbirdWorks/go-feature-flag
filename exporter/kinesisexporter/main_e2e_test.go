//go:build docker

package kinesisexporter_test

import (
	"os"
	"testing"

	"github.com/blackbirdworks/semaphore/testutils/gopherstack"
)

// TestMain removes the gopherstack container shared by the tests of this package.
func TestMain(m *testing.M) {
	code := m.Run()
	gopherstack.TerminateShared()
	os.Exit(code)
}

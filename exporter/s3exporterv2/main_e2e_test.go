//go:build docker

package s3exporterv2_test

import (
	"os"
	"testing"

	"github.com/thomaspoignant/go-feature-flag/testutils/gopherstack"
)

// TestMain removes the gopherstack container shared by the tests of this package.
func TestMain(m *testing.M) {
	code := m.Run()
	gopherstack.TerminateShared()
	os.Exit(code)
}

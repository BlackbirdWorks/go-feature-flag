//go:build !docker

package publish_test

import (
	"testing"

	"github.com/blackbirdworks/semaphore/modules/core/testhelpers/clocktest"
)

// TestMain pins the clock once for the package: the manifest stamps createdAt with clock.Now.
func TestMain(m *testing.M) {
	clocktest.Freeze()
	m.Run()
}

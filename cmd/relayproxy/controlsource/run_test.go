//go:build !docker

package controlsource_test

import (
	"testing"

	"github.com/blackbirdworks/semaphore/modules/core/testhelpers/clocktest"
)

func run(m *testing.M) int {
	clocktest.Freeze()

	return m.Run()
}

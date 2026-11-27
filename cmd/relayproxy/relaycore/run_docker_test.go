//go:build docker

package relaycore_test

import (
	"testing"

	"github.com/blackbirdworks/semaphore/modules/core/testhelpers/clocktest"
	"github.com/blackbirdworks/semaphore/testhelpers/gopherstack"
)

func run(m *testing.M) int {
	clocktest.Freeze()

	return gopherstack.Main(m)
}

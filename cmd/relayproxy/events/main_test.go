//go:build !docker

package events_test

import (
	"testing"

	"github.com/blackbirdworks/semaphore/modules/core/testhelpers/clocktest"
)

func TestMain(m *testing.M) {
	clocktest.FreezeStepping(clockStep)
	m.Run()
}

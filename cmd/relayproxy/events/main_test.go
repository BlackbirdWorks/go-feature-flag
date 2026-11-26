//go:build !docker

package events_test

import (
	"os"
	"testing"

	"github.com/blackbirdworks/semaphore/modules/core/testhelpers/clocktest"
)

func TestMain(m *testing.M) {
	clocktest.FreezeStepping(clockStep)
	os.Exit(exitAfterLeakCheck(m.Run()))
}

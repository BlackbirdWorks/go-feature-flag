package snapshot_test

import (
	"errors"
	"testing"

	"github.com/blackbirdworks/semaphore/modules/core/testhelpers/clocktest"
)

func TestMain(m *testing.M) {
	clocktest.Freeze()

	m.Run()
}

const segAlpha = "alpha"

var errBoom = errors.New("boom")

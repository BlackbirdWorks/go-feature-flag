package valkeysegment_test

import (
	"bytes"
	"fmt"
	"os"
	"runtime/pprof"
	"testing"
	"time"

	"github.com/blackbirdworks/semaphore/modules/core/testhelpers/clocktest"
)

// callStep is the step of the stepping clock: every read of the clock is at least this much after the previous one.
const callStep = time.Millisecond

func TestMain(m *testing.M) {
	clocktest.FreezeStepping(callStep)

	code := m.Run()

	var stacks bytes.Buffer

	_ = pprof.Lookup("goroutine").WriteTo(&stacks, 2)

	if bytes.Contains(stacks.Bytes(), []byte("valkeysegment.(*Provider)")) {
		fmt.Fprintln(os.Stderr, "goroutine leak: a call of the provider is still running")

		code = 1
	}

	os.Exit(code)
}

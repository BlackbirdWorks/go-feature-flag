package valkeysegment_test

import (
	"bytes"
	"fmt"
	"os"
	"runtime/pprof"
	"strings"
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

	if owned := ownedGoroutines(stacks.String()); len(owned) > 0 {
		fmt.Fprintln(os.Stderr, "goroutine leak: code of the provider is still running:", owned)

		code = 1
	}

	os.Exit(code)
}

// ownedGoroutines returns the first frame of each goroutine that runs code of the package, test files excluded.
func ownedGoroutines(dump string) []string {
	var owned []string

	for goroutine := range strings.SplitSeq(dump, "\n\n") {
		for line := range strings.SplitSeq(goroutine, "\n") {
			if strings.Contains(line, "/segment/valkeysegment/") && strings.Contains(line, ".go:") &&
				!strings.Contains(line, "_test.go") {
				owned = append(owned, strings.TrimSpace(line))

				break
			}
		}
	}

	return owned
}

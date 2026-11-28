package segmentadmin_test

import (
	"bytes"
	"fmt"
	"os"
	"runtime/pprof"
	"strings"
	"testing"

	"github.com/blackbirdworks/semaphore/modules/core/testhelpers/clocktest"
)

func TestMain(m *testing.M) {
	clocktest.Freeze()

	code := m.Run()

	var stacks bytes.Buffer

	_ = pprof.Lookup("goroutine").WriteTo(&stacks, 2)

	for _, lib := range []string{"valkey-go", "go-redis"} {
		if strings.Contains(stacks.String(), lib) {
			fmt.Fprintln(os.Stderr, "goroutine leak: a client of", lib, "is still running")

			code = 1
		}
	}

	os.Exit(code)
}

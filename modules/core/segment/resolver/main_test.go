package resolver_test

import (
	"bytes"
	"fmt"
	"os"
	"runtime/pprof"
	"testing"

	"github.com/blackbirdworks/semaphore/modules/core/testhelpers/clocktest"
)

func TestMain(m *testing.M) {
	clocktest.Freeze()

	code := m.Run()

	var stacks bytes.Buffer

	_ = pprof.Lookup("goroutine").WriteTo(&stacks, 2)

	if bytes.Contains(stacks.Bytes(), []byte("resolver.(*Resolver)")) {
		fmt.Fprintln(os.Stderr, "goroutine leak: the resolver still has a call running")

		code = 1
	}

	os.Exit(code)
}

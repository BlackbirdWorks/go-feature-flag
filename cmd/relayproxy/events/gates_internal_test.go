package events

import (
	"context"
	"testing"
	"time"
)

// safeClose closes a gate that may be closed already.
func safeClose(gate chan struct{}) {
	defer func() { _ = recover() }()

	close(gate)
}

// newGate returns a gate that blocks the workers of a test until the test opens it. It is opened when the test
// ends, whatever the outcome, so that a failure cannot leave a worker blocked: register it before anything that
// can fail.
func newGate(tb testing.TB) chan struct{} {
	tb.Helper()

	gate := make(chan struct{})

	tb.Cleanup(func() { safeClose(gate) })

	return gate
}

// openOnCleanup opens the gates of a table row when the test ends.
func openOnCleanup(tb testing.TB, gates ...chan struct{}) {
	tb.Helper()

	tb.Cleanup(func() {
		for _, gate := range gates {
			if gate != nil {
				safeClose(gate)
			}
		}
	})
}

// shutdownOnCleanup shuts a runtime down when the test ends and waits for it for a bounded time: a shutdown that
// waits for a worker held on a gate cannot hang the test, the gates are opened by their own cleanups, which run
// after this one.
func shutdownOnCleanup(tb testing.TB, runtime *Runtime) {
	tb.Helper()

	tb.Cleanup(func() {
		done := make(chan struct{})

		go func() {
			defer close(done)

			_, _ = runtime.Shutdown(context.Background())
		}()

		select {
		case <-done:
		case <-time.After(10 * time.Second):
			tb.Log("the shutdown is held; it ends when the gates of the test are opened")
		}
	})
}

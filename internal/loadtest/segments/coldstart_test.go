//go:build docker

package segments_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestColdStart_FirstRequestsOnAHealthyStore starts a relay with the default connection settings (lookup timeout of
// the provider: 100 ms) against a healthy store and sends a burst of first requests at once. It records how many
// answer STORE_ERROR (logged: it depends on how fast the machine dials) and that a healthy answer comes within a bound.
func TestColdStart_FirstRequestsOnAHealthyStore(t *testing.T) {
	t.Setenv(serialMarker, "1")

	backend := startStore(t)
	backend.add(t, "aud", "alice")
	backend.add(t, "snap", "alice")

	started := time.Now()
	r := startRig(t, &rigOptions{
		address: backend.address, settings: "forceSingleClient: true", resolver: chaosResolver(5),
		flags: chaosFlags, segments: chaosSegments,
	})

	const burst = 100

	var (
		wg        sync.WaitGroup
		storeErrs atomic.Int64
		failed    atomic.Int64
	)

	for range burst {
		wg.Go(func() {
			got, err := r.eval(lookupFlag, "alice")
			if err != nil {
				failed.Add(1)

				return
			}

			if isStoreErr(got) {
				storeErrs.Add(1)
			}
		})
	}

	wg.Wait()
	t.Logf("burst of %d first requests %s after the relay started: %d STORE_ERROR, %d failed", burst,
		time.Since(started).Round(time.Millisecond), storeErrs.Load(), failed.Load())

	// The connection is dialled when the first generation binds, not at the next tick of the retry loop (1 s): it takes
	// the dial of a local store, a few milliseconds. Before that fix the first healthy answer came a retry interval
	// after the start, and the whole burst answered STORE_ERROR. dialBound is half the retry interval.
	const dialBound = 500 * time.Millisecond

	require.Eventually(t, func() bool {
		got, err := r.eval(lookupFlag, "alice")

		return err == nil && isMember(got)
	}, dialBound, tick, "a healthy store answers within %s of the start", dialBound)

	t.Logf("first healthy answer %s after the start", time.Since(started).Round(time.Millisecond))
	assert.Zero(t, failed.Load(), "no request failed")
}

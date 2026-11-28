//go:build docker && bench

package segmentadmin_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment/redissegment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/segmentadmin"
	"github.com/blackbirdworks/semaphore/modules/core/segment/segmenttest"
)

const benchIDs = 2_000_000

// TestImportLarge replaces a set of two million ids by another on a real Valkey and logs the duration, the peak
// memory of the server and the longest wait of a command that ran meanwhile. Run it with `task segment-bench`.
func TestImportLarge(t *testing.T) {
	t.Parallel()

	srv := segmenttest.StartServer(t, segmenttest.ValkeyImage)
	back := makers()[0].build(t, srv, e2eSegments())
	ctx := t.Context()
	target := e2eTarget(segSwap, redissegment.LayoutSet)

	begin := time.Now()
	_, err := segmentadmin.Import(ctx, back.store, target, newReader(t, &idSource{prefix: prefixOld, n: benchIDs}),
		segmentadmin.ImportOptions{})
	require.NoError(t, err)
	t.Logf("first import of %d ids: %s", benchIDs, time.Since(begin).Round(time.Millisecond))

	before, err := back.info(ctx, "used_memory")
	require.NoError(t, err)

	var (
		stop    = make(chan struct{})
		wg      sync.WaitGroup
		slowest atomic.Int64
		failed  firstError
	)

	wg.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
			}

			start := time.Now()

			pingErr := back.store.Ping(ctx)
			if pingErr != nil {
				failed.set(pingErr)

				return
			}

			if took := time.Since(start).Nanoseconds(); took > slowest.Load() {
				slowest.Store(took)
			}
		}
	})

	begin = time.Now()
	rep, err := segmentadmin.Import(ctx, back.store, target, newReader(t, &idSource{prefix: prefixNew, n: benchIDs}),
		segmentadmin.ImportOptions{})
	took := time.Since(begin)

	close(stop)
	wg.Wait()
	require.NoError(t, err)
	require.NoError(t, failed.get())
	assert.Equal(t, int64(benchIDs), rep.Added)

	peak, err := back.info(ctx, "used_memory_peak")
	require.NoError(t, err)

	t.Logf("replacement of %d ids by %d ids: %s", benchIDs, benchIDs, took.Round(time.Millisecond))
	t.Logf("server memory: %d MiB with the old set, peak %d MiB during the rebuild",
		before>>20, peak>>20)
	t.Logf("longest wait of a PING during the import: %s", time.Duration(slowest.Load()).Round(time.Millisecond))
}

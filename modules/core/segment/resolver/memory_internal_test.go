package resolver

import (
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

// TestCacheEntryMemory measures the heap that a full cache holds for each entry, for three sizes of id. The numbers
// are in the documentation: the test pins them to a generous ceiling, so a change that makes an entry much larger
// is seen.
//
//nolint:paralleltest // the heap is read from the whole process
func TestCacheEntryMemory(t *testing.T) {
	const entries = 20000

	for _, idBytes := range []int{16, 64, 512} {
		cch := newCache(SegmentOptions{MaxEntries: entries, Shards: 16, TTL: time.Hour, NegativeTTL: time.Hour})
		keys := make([]string, entries)

		for i := range keys {
			keys[i] = strings.Repeat("x", idBytes-len(strconv.Itoa(i))) + strconv.Itoa(i)
		}

		runtime.GC()

		var before, after runtime.MemStats

		runtime.ReadMemStats(&before)

		for _, key := range keys { // the keys exist already: the cache keeps the same strings
			cch.put(key, segment.Member, baseTime())
		}

		runtime.GC()
		runtime.ReadMemStats(&after)

		held := cch.len()
		perEntry := (float64(after.HeapAlloc) - float64(before.HeapAlloc)) / float64(held)

		assert.LessOrEqual(t, held, entries, "the bound")
		assert.Greater(t, held, entries*9/10, "the shards are almost full")
		assert.Less(
			t,
			perEntry,
			200.0,
			"the bytes of the cache for an entry, without the id that the caller owns (%d byte ids)",
			idBytes,
		)

		runtime.KeepAlive(keys)
	}
}

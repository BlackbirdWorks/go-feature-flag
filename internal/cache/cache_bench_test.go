package cache_test

import (
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/blackbirdworks/semaphore/internal/cache"
	"github.com/blackbirdworks/semaphore/modules/core/dto"
	"github.com/blackbirdworks/semaphore/modules/core/flag"
)

const (
	flagFive = "flag-5"
)

func benchFlags(b *testing.B, n int) map[string]dto.DTO {
	b.Helper()

	return benchFlagsMap(b, n)
}

func benchFlagsMap(tb testing.TB, n int) map[string]dto.DTO {
	tb.Helper()

	buf := make([]byte, 0, n*128)
	for i := range n {
		buf = append(buf, fmt.Sprintf(`flag-%d:
  variations: {A: true, B: false}
  targeting:
    - query: key eq "user-%d"
      variation: A
  defaultRule:
    percentage: {A: 50, B: 50}
`, i, i)...)
	}

	var flags map[string]dto.DTO
	require.NoError(tb, yaml.Unmarshal(buf, &flags))

	return flags
}

// BenchmarkGetFlag measures the flag lookup done on every evaluation.
func BenchmarkGetFlag(b *testing.B) {
	for _, numFlags := range []int{10, 1000} {
		flags := benchFlags(b, numFlags)
		b.Run(fmt.Sprintf("flags=%d/serial", numFlags), func(b *testing.B) {
			cacheMgr := cache.New(nil, "", slog.New(slog.DiscardHandler))
			_ = cacheMgr.UpdateCache(flags, nil, false)

			b.ReportAllocs()
			b.ResetTimer()

			for range b.N {
				_, err := cacheMgr.GetFlag(flagFive)
				if err != nil {
					require.NoError(b, err)
				}
			}
		})
		b.Run(fmt.Sprintf("flags=%d/parallel", numFlags), func(b *testing.B) {
			cacheMgr := cache.New(nil, "", slog.New(slog.DiscardHandler))
			_ = cacheMgr.UpdateCache(flags, nil, false)

			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					_, err := cacheMgr.GetFlag(flagFive)
					if err != nil {
						assert.NoError(b, err)

						return
					}
				}
			})
		})
		b.Run(fmt.Sprintf("flags=%d/parallel-with-updates", numFlags), func(b *testing.B) {
			cacheMgr := cache.New(nil, "", slog.New(slog.DiscardHandler))
			_ = cacheMgr.UpdateCache(flags, nil, false)

			var (
				stop atomic.Bool
				wg   sync.WaitGroup
			)
			wg.Go(func() {
				for !stop.Load() {
					_ = cacheMgr.UpdateCache(flags, nil, false)
				}
			})
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				var cachedFlag flag.Flag

				for pb.Next() {
					var err error

					cachedFlag, err = cacheMgr.GetFlag(flagFive)
					if err != nil {
						assert.NoError(b, err)

						return
					}
				}

				_ = cachedFlag
			})
			b.StopTimer()
			stop.Store(true)
			wg.Wait()
		})
	}
}

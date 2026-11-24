package cache_test

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/blackbirdworks/semaphore/internal/cache"
	"github.com/blackbirdworks/semaphore/modules/core/dto"
	"github.com/blackbirdworks/semaphore/modules/core/flag"
	"github.com/blackbirdworks/semaphore/utils/fflog"
	"gopkg.in/yaml.v3"
)

func benchFlags(b *testing.B, n int) map[string]dto.DTO {
	b.Helper()
	return benchFlagsMap(b, n)
}

func benchFlagsMap(tb testing.TB, n int) map[string]dto.DTO {
	tb.Helper()
	var sb []byte
	for i := 0; i < n; i++ {
		sb = append(sb, fmt.Sprintf(`flag-%d:
  variations: {A: true, B: false}
  targeting:
    - query: key eq "user-%d"
      variation: A
  defaultRule:
    percentage: {A: 50, B: 50}
`, i, i)...)
	}
	var flags map[string]dto.DTO
	if err := yaml.Unmarshal(sb, &flags); err != nil {
		tb.Fatal(err)
	}
	return flags
}

// BenchmarkGetFlag measures the flag lookup done on every evaluation.
func BenchmarkGetFlag(b *testing.B) {
	for _, n := range []int{10, 1000} {
		flags := benchFlags(b, n)
		b.Run(fmt.Sprintf("flags=%d/serial", n), func(b *testing.B) {
			m := cache.New(nil, "", &fflog.FFLogger{})
			_ = m.UpdateCache(flags, nil, false)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := m.GetFlag("flag-5"); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("flags=%d/parallel", n), func(b *testing.B) {
			m := cache.New(nil, "", &fflog.FFLogger{})
			_ = m.UpdateCache(flags, nil, false)
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					if _, err := m.GetFlag("flag-5"); err != nil {
						b.Error(err)
						return
					}
				}
			})
		})
		b.Run(fmt.Sprintf("flags=%d/parallel-with-updates", n), func(b *testing.B) {
			m := cache.New(nil, "", &fflog.FFLogger{})
			_ = m.UpdateCache(flags, nil, false)
			var stop atomic.Bool
			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				defer wg.Done()
				for !stop.Load() {
					_ = m.UpdateCache(flags, nil, false)
				}
			}()
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				var f flag.Flag
				for pb.Next() {
					var err error
					if f, err = m.GetFlag("flag-5"); err != nil {
						b.Error(err)
						return
					}
				}
				_ = f
			})
			b.StopTimer()
			stop.Store(true)
			wg.Wait()
		})
	}
}

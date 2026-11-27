package resolver_test

import (
	"context"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/resolver"
)

func benchLookups(count int) []segment.Lookup {
	lookups := make([]segment.Lookup, count)
	for i := range lookups {
		lookups[i] = lk(segBeta, "id"+strconv.Itoa(i))
	}

	return lookups
}

func BenchmarkResolverHit(b *testing.B) {
	res, err := resolver.New(betaStore(), &resolver.Options{})
	require.NoError(b, err)

	lookups := benchLookups(8)
	ctx := context.Background()

	require.NoError(b, res.Contains(ctx, lookups))
	b.ReportAllocs()

	for b.Loop() {
		_ = res.Contains(ctx, lookups)
	}
}

func BenchmarkResolverMiss(b *testing.B) {
	for _, count := range []int{1, 8} {
		b.Run(strconv.Itoa(count)+"lookups", func(b *testing.B) {
			res, err := resolver.New(betaStore(), &resolver.Options{Default: resolver.SegmentOptions{NoCache: true}})
			require.NoError(b, err)

			lookups := benchLookups(count)
			ctx := context.Background()

			b.ReportAllocs()

			for b.Loop() {
				_ = res.Contains(ctx, lookups)
			}
		})
	}
}

// BenchmarkResolverHitParallel compares one lock with sixteen under contention: run with -cpu 1,4,8.
func BenchmarkResolverHitParallel(b *testing.B) {
	for _, shards := range []int{1, 16} {
		b.Run(strconv.Itoa(shards)+"shards", func(b *testing.B) {
			res, err := resolver.New(betaStore(), &resolver.Options{Default: resolver.SegmentOptions{Shards: shards}})
			require.NoError(b, err)

			ctx := context.Background()

			require.NoError(b, res.Contains(ctx, benchLookups(64)))
			b.ReportAllocs()
			b.RunParallel(func(pb *testing.PB) {
				lookups := benchLookups(8)
				for pb.Next() {
					_ = res.Contains(ctx, lookups)
				}
			})
		})
	}
}

func BenchmarkResolverBreakerOpen(b *testing.B) {
	store := &fakeStore{fail: errStore}
	res, err := resolver.New(
		store,
		&resolver.Options{Default: resolver.SegmentOptions{BreakerFailures: 1, NoCache: true}},
	)
	require.NoError(b, err)

	ctx := context.Background()
	lookups := benchLookups(1)

	_ = res.Contains(ctx, lookups) // opens the breaker

	b.ReportAllocs()

	for b.Loop() {
		_ = res.Contains(ctx, lookups)
	}
}

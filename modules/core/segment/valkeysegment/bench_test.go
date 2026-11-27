//go:build docker && bench

package valkeysegment_test

import (
	"context"
	"strconv"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/redissegment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/valkeysegment"
)

// BenchmarkContains compares the go-redis provider (type redis) and the valkey-go provider (type valkey) on one
// Valkey node, for 1, 10 and 100 ids of a set segment in one call, one caller and many concurrent callers, with
// the two pipelining modes of the valkey provider. Run it with -cpu 1,4,8 for the scaling of the parallel runs.
func BenchmarkContains(b *testing.B) {
	srv := startServerImage(b, valkeyImage)
	opt := srv.defaultOption() // both providers use their default connection and pool settings
	client := seed(b, opt)

	members := make([]string, 0, 50)
	for i := 0; i < 100; i += 2 {
		members = append(members, "id"+strconv.Itoa(i))
	}

	load(b, client, keyBeta, members...)

	providers := make([]struct {
		prov segment.Provider
		name string
	}, 0, 3)

	modes := []valkeysegment.Pipelining{
		valkeysegment.PipeliningAlways,
		valkeysegment.PipeliningAuto,
	}

	for _, mode := range modes {
		providers = append(providers, struct {
			prov segment.Provider
			name string
		}{
			e2eProvider(b, opt, &valkeysegment.Options{MissingKey: valkeysegment.MissingUnknown,
				Segments: map[string]valkeysegment.SegmentOptions{
					segBeta: {Key: keyBeta},
				},
				Timeout:    e2eTimeout,
				Pipelining: mode,
			}),
			"valkey-" + string(mode),
		})
	}

	redisProv, dialErr := redissegment.Dial(
		&redis.UniversalOptions{Addrs: []string{srv.address}},
		&redissegment.Options{
			Segments: map[string]redissegment.SegmentOptions{segBeta: {Key: keyBeta}}, Timeout: e2eTimeout,
		},
	)
	require.NoError(b, dialErr)
	b.Cleanup(func() { _ = redisProv.Close() })

	providers = append(providers, struct {
		prov segment.Provider
		name string
	}{redisProv, "redis"})

	for _, size := range []int{1, 10, 100} {
		for _, each := range providers {
			b.Run(each.name+"/ids="+strconv.Itoa(size), func(b *testing.B) {
				lookups := make([]segment.Lookup, size)
				for i := range lookups {
					lookups[i] = lk(segBeta, "id"+strconv.Itoa(i))
				}

				ctx := context.Background()

				require.NoError(b, each.prov.Contains(ctx, lookups)) // warm the connection

				b.ReportAllocs()
				b.ResetTimer()

				for range b.N {
					require.NoError(b, each.prov.Contains(ctx, lookups))
				}
			})

			b.Run(each.name+"/ids="+strconv.Itoa(size)+"/parallel", func(b *testing.B) {
				ctx := context.Background()

				b.ReportAllocs()
				b.ResetTimer()
				b.RunParallel(func(pb *testing.PB) {
					lookups := make([]segment.Lookup, size)
					for i := range lookups {
						lookups[i] = lk(segBeta, "id"+strconv.Itoa(i))
					}

					for pb.Next() {
						if !assert.NoError(b, each.prov.Contains(ctx, lookups)) {
							return
						}
					}
				})
			})
		}
	}
}

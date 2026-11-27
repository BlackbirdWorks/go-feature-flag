package resolver_test

import (
	"context"
	"math"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/resolver"
)

// cheapestAllocs is the number of allocations of f in its cheapest run among several single runs, so a stray
// allocation of the runtime does not decide the result.
func cheapestAllocs(f func()) float64 {
	cheapest := math.MaxFloat64

	for range 30 {
		cheapest = min(cheapest, testing.AllocsPerRun(1, f))
	}

	return cheapest
}

// TestCacheHitAllocatesNothing pins the hit path: every lookup answered by the cache, with or without an observer.
// The results are kept in a sink, so the compiler cannot drop the call, and a control row that must allocate proves
// that the measurement sees allocations.
//
//nolint:paralleltest // AllocsPerRun counts allocations from concurrent tests across the process
func TestCacheHitAllocatesNothing(t *testing.T) {
	tests := []struct {
		opts       *resolver.Options
		name       string
		wantAllocs bool
	}{
		{name: "no observer", opts: &resolver.Options{}},
		{name: "counters", opts: &resolver.Options{Observer: &resolver.Counters{}}},
		{
			name: "trimLower with ids already normal",
			opts: &resolver.Options{Default: resolver.SegmentOptions{Normalize: resolver.NormalizeTrimLower}},
		},
		{
			name:       "control: a miss allocates",
			opts:       &resolver.Options{Default: resolver.SegmentOptions{NoCache: true}},
			wantAllocs: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := newResolver(t, betaStore(), tt.opts)
			lookups := make([]segment.Lookup, 8)

			for i := range lookups {
				lookups[i] = lk(segBeta, "id"+strconv.Itoa(i))
			}

			ctx := context.Background()

			require.NoError(t, res.Contains(ctx, lookups)) // fills the cache

			var (
				errSink   error
				stateSink segment.State
			)

			allocs := cheapestAllocs(func() {
				for i := range lookups {
					lookups[i].State = segment.Unknown
				}

				errSink = res.Contains(ctx, lookups)
				stateSink = lookups[3].State
			})

			require.NoError(t, errSink)
			assert.Equal(t, segment.NotMember, stateSink)

			if tt.wantAllocs {
				assert.Positive(t, allocs, "the control must allocate, or the measure is blind")

				return
			}

			assert.InDelta(t, 0, allocs, 0)
		})
	}
}

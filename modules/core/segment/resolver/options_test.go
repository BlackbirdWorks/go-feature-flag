package resolver_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/resolver"
)

func TestNewValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		store   segment.Provider
		wantErr error
		opts    *resolver.Options
		name    string
	}{
		{
			name:  "valid",
			store: &fakeStore{},
			opts:  &resolver.Options{Segments: map[string]resolver.SegmentOptions{segBeta: {TTL: time.Second}}},
		},
		{name: "nil provider", wantErr: resolver.ErrNilProvider},
		{
			name:    "negative call timeout",
			store:   &fakeStore{},
			opts:    &resolver.Options{CallTimeout: -1},
			wantErr: resolver.ErrInvalidOptions,
		},
		{
			name:    "bad segment name",
			store:   &fakeStore{},
			opts:    &resolver.Options{Segments: map[string]resolver.SegmentOptions{"Bad Name": {}}},
			wantErr: resolver.ErrInvalidOptions,
		},
		{
			name:    "bad normalization",
			store:   &fakeStore{},
			opts:    &resolver.Options{Default: resolver.SegmentOptions{Normalize: "x"}},
			wantErr: resolver.ErrInvalidOptions,
		},
		{
			name:    "negative ttl",
			store:   &fakeStore{},
			opts:    &resolver.Options{Default: resolver.SegmentOptions{TTL: -1}},
			wantErr: resolver.ErrInvalidOptions,
		},
		{
			name:    "negative entries",
			store:   &fakeStore{},
			opts:    &resolver.Options{Default: resolver.SegmentOptions{MaxEntries: -1}},
			wantErr: resolver.ErrInvalidOptions,
		},
		{
			name:    "negative breaker failures",
			store:   &fakeStore{},
			opts:    &resolver.Options{Default: resolver.SegmentOptions{BreakerFailures: -1}},
			wantErr: resolver.ErrInvalidOptions,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			res, err := resolver.New(tt.store, tt.opts)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, res)

				return
			}

			require.NoError(t, err)
			assert.NotNil(t, res)
		})
	}
}

func TestCountersAndObserver(t *testing.T) {
	t.Parallel()

	counters := &resolver.Counters{}
	res := newResolver(t, betaStore(), &resolver.Options{Observer: counters})

	require.NoError(t, res.Contains(context.Background(), []segment.Lookup{lk(segBeta, idAlice), lk(segBeta, idZed)}))
	require.NoError(t, res.Contains(context.Background(), []segment.Lookup{lk(segBeta, idAlice), lk("other", "x")}))

	stats := counters.Snapshot()

	assert.Equal(t, resolver.Stats{
		Calls: 2, Lookups: 4, Hits: 1, Misses: 3, Unknown: 1,
		Duration: stats.Duration,
	}, stats)
	assert.GreaterOrEqual(t, stats.Duration, time.Duration(0))
}

func TestCountersIgnoreNegativeValues(t *testing.T) {
	t.Parallel()

	counters := &resolver.Counters{}
	counters.ObserveCall(
		resolver.CallObservation{
			Lookups:        -1,
			Hits:           -1,
			Misses:         -1,
			Coalesced:      -1,
			ShortCircuited: -1,
			Unknown:        -1,
			Duration:       -1,
		},
	)

	assert.Equal(t, resolver.Stats{Calls: 1}, counters.Snapshot())
}

func TestContainsConcurrentCallers(t *testing.T) {
	t.Parallel()

	store := betaStore()
	res := newResolver(t, store, &resolver.Options{})

	const callers = 16

	var wg sync.WaitGroup

	got := make([][]segment.State, callers)
	errs := make([]error, callers)

	for caller := range callers {
		wg.Go(func() {
			lookups := []segment.Lookup{lk(segBeta, idAlice), lk(segBeta, idZed)}
			errs[caller] = res.Contains(context.Background(), lookups)
			got[caller] = states(lookups)
		})
	}

	wg.Wait()

	for caller := range callers {
		require.NoError(t, errs[caller])
		assert.Equal(t, []segment.State{segment.Member, segment.NotMember}, got[caller])
	}
}

package resolver_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/resolver"
)

// A caller that leaves is not a failure of the store: the call is counted as unknown with the reason waiting, not as
// failed, and the breaker of the segment does not move. A store that fails is both.
func TestACallerThatLeavesIsNotAStoreFailure(t *testing.T) {
	t.Parallel()

	const requests = 5

	tests := []struct {
		name         string
		wantFailed   uint64
		wantUnknown  uint64
		wantFailures int
		wantState    segment.State
		fail         bool
		leaveWaiting bool
	}{
		{
			name: "cancelled before the call", wantUnknown: requests, wantState: segment.Unknown,
		},
		{
			name:         "cancelled while waiting for the store",
			leaveWaiting: true,
			wantUnknown:  1,
			wantState:    segment.Unknown,
		},
		{
			name:         "the store fails",
			fail:         true,
			wantFailed:   1,
			wantFailures: 1,
			wantUnknown:  1,
			wantState:    segment.Unknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := betaStore()
			counters := &resolver.Counters{}
			res := newResolver(t, store, &resolver.Options{Observer: counters})

			var got []segment.State

			switch {
			case tt.fail:
				store.Fail()

				lookups := []segment.Lookup{lk(segBeta, idAlice)}
				require.Error(t, res.Contains(context.Background(), lookups))

				got = states(lookups)
			case tt.leaveWaiting:
				store.gate = gateFor(t)
				store.entered = make(chan struct{}, 1)
				ctx, cancel := context.WithCancel(context.Background())
				lookups := []segment.Lookup{lk(segBeta, idAlice)}
				done := make(chan struct{})

				go func() {
					defer close(done)

					assert.ErrorIs(t, res.Contains(ctx, lookups), resolver.ErrWaiting)
				}()

				<-store.entered
				cancel()
				waitLeft(t, done, store.gate)

				got = states(lookups)
			default:
				gone, cancel := context.WithCancel(context.Background())
				cancel()

				for range requests {
					lookups := []segment.Lookup{lk(segBeta, idAlice)}
					require.ErrorIs(t, res.Contains(gone, lookups), resolver.ErrWaiting)

					got = states(lookups)
				}
			}

			stats := counters.Snapshot()
			status := res.Status().Segments[segBeta]

			assert.Equal(t, []segment.State{tt.wantState}, got)
			assert.Equal(t, tt.wantFailed, stats.Failed)
			assert.Equal(t, tt.wantUnknown, stats.Unknown)
			assert.Equal(t, tt.wantFailures, status.Failures)
			assert.Equal(t, resolver.BreakerClosed, status.Breaker)
		})
	}
}

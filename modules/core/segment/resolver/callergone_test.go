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

const (
	segSlow = "slow"
	segBad  = "bad"
)

// routedStore holds the lookups of the segment segSlow until its gate opens, and fails the ones of segBad when its gate
// opens.
type routedStore struct {
	slowGate chan struct{}
	badGate  chan struct{}
	entered  chan string
}

func (s *routedStore) Contains(ctx context.Context, lookups []segment.Lookup) error {
	name := lookups[0].Segment
	s.entered <- name

	gate := s.slowGate
	if name == segBad {
		gate = s.badGate
	}

	select {
	case <-gate:
	case <-ctx.Done():
		return ctx.Err()
	}

	if name == segBad {
		return errStore
	}

	for i := range lookups {
		lookups[i].State = segment.Member
	}

	return nil
}

// failureHook counts the calls and runs a hook when a call failed.
type failureHook struct {
	*resolver.Counters

	onFailed func()
}

// ObserveSegment runs the hook when the lookups of a segment stayed unknown because the store failed.
func (h *failureHook) ObserveSegment(seg resolver.SegmentObservation) {
	h.Counters.ObserveSegment(seg)

	if seg.Failure == resolver.ReasonStore && h.onFailed != nil {
		h.onFailed()
	}
}

// A caller that leaves while the store failed another lookup of its call is a failed call: the failure of the store is
// not hidden by the departure, whether the failed lookup is the one that the caller waited for first or one that was
// already answered when it left.
func TestACallerThatLeavesAfterAStoreFailureIsAFailedCall(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		order      []string
		wantFailed uint64
	}{
		{name: "the failed lookup is awaited first", order: []string{segBad, segSlow}, wantFailed: 2},
		{
			name:       "the failed lookup is answered while the caller waits for the other",
			order:      []string{segSlow, segBad},
			wantFailed: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &routedStore{
				slowGate: make(chan struct{}), badGate: make(chan struct{}), entered: make(chan string, 8),
			}
			hook := &failureHook{Counters: &resolver.Counters{}}
			res := newResolver(t, store, &resolver.Options{Observer: hook})

			var wg sync.WaitGroup

			t.Cleanup(wg.Wait)
			t.Cleanup(func() {
				close(store.slowGate)

				select {
				case <-store.badGate:
				default:
					close(store.badGate)
				}
			})

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			hook.onFailed = cancel

			// one caller holds the flight of segSlow, another the flight of segBad
			wg.Go(func() { _ = res.Contains(context.Background(), []segment.Lookup{lk(segSlow, idAlice)}) })
			require.Equal(t, segSlow, <-store.entered)

			wg.Go(func() { _ = res.Contains(context.Background(), []segment.Lookup{lk(segBad, idAlice)}) })
			require.Equal(t, segBad, <-store.entered)

			// the caller of the test joins both flights; the failure of the second ends its context
			lookups := make([]segment.Lookup, len(tt.order))
			for i, name := range tt.order {
				lookups[i] = lk(name, idAlice)
			}

			done := make(chan error, 1)

			go func() { done <- res.Contains(ctx, lookups) }()

			waitForJoin(t, res, segSlow)
			waitForJoin(t, res, segBad)
			close(store.badGate)

			select {
			case err := <-done:
				require.ErrorIs(t, err, resolver.ErrWaiting)
			case <-time.After(leaveBound):
				require.FailNow(t, "the caller was held past the end of its context")
			}

			assert.Equal(t, tt.wantFailed, hook.Snapshot().Failed)
		})
	}
}

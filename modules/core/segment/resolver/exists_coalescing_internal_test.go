package resolver

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

const testOther = "other"

// hungExistsStore answers the lookups at once (Member for the segment testOther, Unknown for any other, so the
// resolver asks Exists) and never returns from Exists until it is released, whatever its context says.
type hungExistsStore struct {
	gate     chan struct{}
	entered  chan struct{}
	calls    []string
	mu       sync.Mutex
	once     sync.Once
	existsOf atomic.Int64
}

func (s *hungExistsStore) Contains(_ context.Context, lookups []segment.Lookup) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range lookups {
		s.calls = append(s.calls, lookups[i].Segment+":"+lookups[i].ID)

		if lookups[i].Segment == testOther {
			lookups[i].State = segment.Member
		}
	}

	return nil
}

func (s *hungExistsStore) Exists(
	_ context.Context,
	names []string,
) ([]segment.State, error) { //nolint:unparam // the interface returns an error
	s.existsOf.Add(1)

	select {
	case s.entered <- struct{}{}:
	default:
	}

	<-s.gate

	return make([]segment.State, len(names)), nil
}

func (s *hungExistsStore) release() { s.once.Do(func() { close(s.gate) }) }

func (s *hungExistsStore) callList() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]string(nil), s.calls...)
}

// Only the existence check has a deadline that has passed when it starts (the call timeout of the lookups is the
// default): the membership lookups are never overdue, the test waits on the overdue count, not on time.
func TestAHungExistenceCheckIsOnePerSegment(t *testing.T) {
	t.Parallel()

	const (
		wantExistsCalls = 1
		wantOverdue     = 1
	)

	store := &hungExistsStore{gate: make(chan struct{}), entered: make(chan struct{}, 1)}
	// the threshold is below the number of lookups: one overdue check for each lookup would refuse the last ones
	res := newInternal(t, store, &Options{MaxOverdue: 2, Default: SegmentOptions{NoCache: true, NoBreaker: true}})
	res.existsTimeout = time.Nanosecond

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), waitBound)
		defer cancel()

		assert.NoError(t, res.Shutdown(ctx))
	})
	t.Cleanup(store.release) // runs first: the worker stuck in the check can end

	ids := []string{"a", "b", "c", "d", "e"}
	wantCalls := make([]string, 0, len(ids)+1)

	for i, id := range ids {
		lookups := []segment.Lookup{{Segment: testSeg, ID: id}}

		require.NoError(t, res.Contains(context.Background(), lookups), "lookup %d", i)
		assert.Equal(t, []segment.State{segment.Unknown}, []segment.State{lookups[0].State})

		if i == 0 {
			select {
			case <-store.entered:
			case <-time.After(waitBound):
				require.Fail(t, "the first check never reached the store")
			}
		}

		// only the worker stuck in the first check is left: the check of this lookup was skipped or has finished
		require.Eventually(t, func() bool { return res.Status().Running == 1 }, waitBound, time.Millisecond)

		wantCalls = append(wantCalls, testSeg+":"+id)
	}

	require.Eventually(t, func() bool { return res.Status().Overdue == wantOverdue }, waitBound, time.Millisecond)
	assert.Equal(t, int64(wantExistsCalls), store.existsOf.Load(), "one check in flight for the segment")

	// an unrelated segment is not refused: nothing but one check is overdue
	other := []segment.Lookup{{Segment: testOther, ID: testAlice}}

	require.NoError(t, res.Contains(context.Background(), other))
	assert.Equal(t, []segment.State{segment.Member}, []segment.State{other[0].State})
	assert.Equal(t, wantOverdue, res.Status().Overdue)
	assert.Equal(t, append(wantCalls, testOther+":"+testAlice), store.callList())
}

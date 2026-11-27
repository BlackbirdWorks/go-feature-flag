package resolver_test

import (
	"bytes"
	"context"
	"runtime/pprof"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/resolver"
)

// hungExists answers no lookup of the segments it does not hold (so the resolver asks Exists) and never returns from
// Exists until it is released, whatever its context says.
type hungExists struct {
	*fakeStore

	gate     chan struct{}
	entered  chan struct{}
	once     sync.Once
	existsOf atomic.Int64
}

func (h *hungExists) Exists(
	_ context.Context,
	names []string,
) ([]segment.State, error) { //nolint:unparam // the interface returns an error
	h.existsOf.Add(1)

	select {
	case h.entered <- struct{}{}:
	default:
	}

	<-h.gate

	return make([]segment.State, len(names)), nil
}

// Release lets the hung checks return (the resolver of the test calls it first when it ends).
func (h *hungExists) Release() { h.once.Do(func() { close(h.gate) }) }

func TestAHungExistenceCheckIsOnePerSegment(t *testing.T) {
	t.Parallel()

	const (
		lookupsOfBeta   = 5
		wantExistsCalls = 1
		wantOverdue     = 1
	)

	store := &hungExists{
		fakeStore: &fakeStore{data: members(map[string][]string{segOther: {idAlice}})},
		gate:      make(chan struct{}),
		entered:   make(chan struct{}, 1),
	}
	// the threshold is below the number of lookups: one overdue check for each lookup would refuse the last ones
	res := newResolver(t, store, &resolver.Options{
		CallTimeout: time.Millisecond,
		MaxOverdue:  2,
		Default:     resolver.SegmentOptions{NoCache: true, NoBreaker: true},
	})

	ids := []string{"a", "b", "c", "d", "e"}
	require.Len(t, ids, lookupsOfBeta)

	for i, id := range ids {
		lookups := []segment.Lookup{lk(segBeta, id)}

		require.NoError(t, res.Contains(context.Background(), lookups), "lookup %d", i)
		assert.Equal(t, []segment.State{segment.Unknown}, states(lookups))

		if i == 0 {
			select {
			case <-store.entered:
			case <-time.After(eventuallyWait):
				require.Fail(t, "the first check never reached the store")
			}
		}

		// only the worker stuck in the first check is left: the check of this lookup was skipped or has finished
		require.Eventually(t, func() bool { return res.Status().Running == 1 }, eventuallyWait, eventuallyTick)
	}

	require.Eventually(t, func() bool { return res.Status().Overdue == wantOverdue }, eventuallyWait, eventuallyTick)
	assert.Equal(t, int64(wantExistsCalls), store.existsOf.Load(), "one check in flight for the segment")

	// the store was asked for every lookup, and an unrelated segment is not refused: nothing but one check is overdue
	other := []segment.Lookup{lk(segOther, idAlice)}

	require.NoError(t, res.Contains(context.Background(), other))
	assert.Equal(t, []segment.State{segment.Member}, states(other))
	assert.Equal(t, wantOverdue, res.Status().Overdue)

	wantCalls := make([][]string, 0, len(ids)+1)
	for _, id := range ids {
		wantCalls = append(wantCalls, []string{segBeta + ":" + id})
	}

	wantCalls = append(wantCalls, []string{segOther + ":" + idAlice})

	assert.Equal(t, wantCalls, store.callList())
}

// stoppedByContext is a store whose Shutdown returns only when its context ends; it records that context's deadline.
type stoppedByContext struct {
	*fakeStore

	deadline chan time.Time
	hasLimit chan bool
	stopped  chan struct{}
}

func (s *stoppedByContext) Shutdown(ctx context.Context) error {
	defer close(s.stopped)

	deadline, ok := ctx.Deadline()
	s.deadline <- deadline

	s.hasLimit <- ok

	<-ctx.Done()

	return ctx.Err()
}

func TestTheShutdownOfTheStoreEndsAtTheFirstDeadlinePlusTheGrace(t *testing.T) {
	t.Parallel()

	const grace = 20 * time.Millisecond

	tests := []struct {
		wantErr           error
		name              string
		callerTimeout     time.Duration
		wantExactDeadline bool
	}{
		{
			name: "a caller with a deadline", callerTimeout: 10 * time.Millisecond,
			wantErr: resolver.ErrShutdown, wantExactDeadline: true,
		},
		{name: "a caller without a deadline", wantErr: context.DeadlineExceeded},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &stoppedByContext{
				fakeStore: betaStore(),
				deadline:  make(chan time.Time, 1),
				hasLimit:  make(chan bool, 1),
				stopped:   make(chan struct{}),
			}

			res, err := resolver.New(store, &resolver.Options{ShutdownGrace: grace})
			require.NoError(t, err)

			label := "shutdown-grace-" + t.Name() // unique: the goroutine of the store inherits it from this one
			ctx := context.Background()

			var callerDeadline time.Time

			if tt.callerTimeout > 0 {
				var cancel context.CancelFunc

				ctx, cancel = context.WithTimeout(ctx, tt.callerTimeout)
				defer cancel()

				callerDeadline, _ = ctx.Deadline()
			}

			done := make(chan error, 1)

			go pprof.Do(ctx, pprof.Labels("test", label), func(ctx context.Context) {
				done <- res.Shutdown(ctx)
			})

			var shutdownErr error

			select {
			case shutdownErr = <-done:
			case <-time.After(eventuallyWait):
				require.Fail(t, "Shutdown did not end: the store is not bounded")
			}

			require.ErrorIs(t, shutdownErr, tt.wantErr)

			select {
			case <-store.stopped:
			case <-time.After(eventuallyWait):
				require.Fail(t, "the shutdown of the store never ended")
			}

			assert.True(t, <-store.hasLimit, "the context of the store has a deadline")

			storeDeadline := <-store.deadline
			if tt.wantExactDeadline {
				assert.Equal(t, callerDeadline.Add(grace), storeDeadline)
			}

			require.Eventually(t, func() bool {
				var dump bytes.Buffer

				_ = pprof.Lookup("goroutine").WriteTo(&dump, 1)

				return !bytes.Contains(dump.Bytes(), []byte(label))
			}, eventuallyWait, eventuallyTick, "no goroutine of the shutdown is left")
		})
	}
}

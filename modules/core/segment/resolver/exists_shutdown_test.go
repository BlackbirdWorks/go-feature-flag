package resolver_test

import (
	"bytes"
	"context"
	"runtime/pprof"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/resolver"
)

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

func TestTheShutdownBoundOfTheStoreIsFixedByTheFirstCallWhoeverRunsIt(t *testing.T) {
	t.Parallel()

	const grace = 20 * time.Millisecond

	store := &stoppedByContext{
		fakeStore: betaStore(),
		deadline:  make(chan time.Time, 1),
		hasLimit:  make(chan bool, 1),
		stopped:   make(chan struct{}),
	}
	entered := store.Hang()

	res, err := resolver.New(store, &resolver.Options{ShutdownGrace: grace})
	require.NoError(t, err)

	lookupDone := make(chan error, 1)

	go func() { lookupDone <- res.Contains(context.Background(), []segment.Lookup{lk(segBeta, idAlice)}) }()

	select {
	case <-entered:
	case <-time.After(eventuallyWait):
		require.Fail(t, "the lookup never reached the store")
	}

	// the first call has a deadline and leaves while the lookup is still in flight
	first, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	firstDeadline, _ := first.Deadline()

	require.ErrorIs(t, res.Shutdown(first), resolver.ErrShutdown)

	// the second call, with no deadline, drains and reaches the store's Shutdown: the bound is still the first one
	store.Release()

	select {
	case lookupErr := <-lookupDone:
		require.NoError(t, lookupErr)
	case <-time.After(eventuallyWait):
		require.Fail(t, "the lookup did not end")
	}

	require.ErrorIs(t, res.Shutdown(context.Background()), context.DeadlineExceeded)

	assert.True(t, <-store.hasLimit)
	assert.Equal(t, firstDeadline.Add(grace), <-store.deadline)
}

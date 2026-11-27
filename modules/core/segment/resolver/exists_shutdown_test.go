package resolver_test

import (
	"bytes"
	"context"
	"runtime/pprof"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/resolver"
)

// farDeadline is a deadline that no run of the tests reaches: a caller's context ends only when the test cancels it,
// so no timer decides the outcome. It is also an exact value to assert on.
func farDeadline() time.Time { return time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC) }

// storeCall is what the store's Shutdown saw of its context.
type storeCall struct {
	deadline time.Time
	hasLimit bool
}

// stoppedByContext is a store whose Shutdown returns when its context ends or when the test releases it; it reports
// the deadline of the context it was given.
type stoppedByContext struct {
	*fakeStore

	entered chan storeCall
	release chan struct{}
	stopped chan struct{}
	once    sync.Once
}

// newStoppedByContext returns the store; its Shutdown is released when the test ends, so a failed assertion leaves
// nothing waiting.
func newStoppedByContext(t *testing.T) *stoppedByContext {
	t.Helper()

	store := &stoppedByContext{
		fakeStore: betaStore(),
		entered:   make(chan storeCall, 1),
		release:   make(chan struct{}),
		stopped:   make(chan struct{}),
	}
	t.Cleanup(store.open)

	return store
}

func (s *stoppedByContext) Shutdown(ctx context.Context) error {
	defer close(s.stopped)

	deadline, ok := ctx.Deadline()
	s.entered <- storeCall{deadline: deadline, hasLimit: ok}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.release:
		return nil
	}
}

func (s *stoppedByContext) open() { s.once.Do(func() { close(s.release) }) }

// receive stores into out what arrives on ch, or fails the test after the failure bound with msg.
func receive[T any](t *testing.T, ch <-chan T, out *T, msg string) {
	t.Helper()

	select {
	case *out = <-ch:
	case <-time.After(eventuallyWait):
		require.FailNow(t, msg)
	}
}

func TestTheShutdownOfTheStoreEndsAtTheFirstDeadlinePlusTheGrace(t *testing.T) {
	t.Parallel()

	const grace = 20 * time.Millisecond

	tests := []struct {
		wantErr error
		// callerContext makes the context of the Shutdown call
		callerContext func() (context.Context, context.CancelFunc)
		wantDeadline  time.Time // zero: the deadline of the store's context is only asserted to exist
		name          string
		// endCaller ends the context of the caller once the store has been reached
		endCaller bool
	}{
		{
			name: "a caller with a deadline",
			callerContext: func() (context.Context, context.CancelFunc) {
				return context.WithDeadline(context.Background(), farDeadline())
			},
			endCaller: true, wantErr: resolver.ErrShutdown, wantDeadline: farDeadline().Add(grace),
		},
		{
			name: "a caller whose context ended before the call and nothing is in flight",
			callerContext: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithDeadline(context.Background(), farDeadline())
				cancel()

				return ctx, cancel
			},
			wantErr: resolver.ErrShutdown, wantDeadline: farDeadline().Add(grace),
		},
		{
			name:          "a caller without a deadline",
			callerContext: func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) },
			wantErr:       context.DeadlineExceeded,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newStoppedByContext(t)

			res, err := resolver.New(store, &resolver.Options{ShutdownGrace: grace})
			require.NoError(t, err)

			label := "shutdown-grace-" + t.Name() // unique: the goroutine of the store inherits it from this one

			ctx, cancel := tt.callerContext()
			defer cancel()

			done := make(chan error, 1)

			go pprof.Do(ctx, pprof.Labels("test", label), func(ctx context.Context) {
				done <- res.Shutdown(ctx)
			})

			// the store is reached whatever the context of the caller: its Shutdown must not be skipped
			var call storeCall

			receive(t, store.entered, &call, "the store's Shutdown was never called")

			assert.True(t, call.hasLimit, "the context of the store has a deadline")

			if !tt.wantDeadline.IsZero() {
				assert.Equal(t, tt.wantDeadline, call.deadline)
			}

			if tt.endCaller {
				cancel()
			}

			var shutdownErr error

			receive(t, done, &shutdownErr, "Shutdown did not end: the store is not bounded")
			require.ErrorIs(t, shutdownErr, tt.wantErr)

			store.open()

			var stopped struct{}

			receive(t, store.stopped, &stopped, "the shutdown of the store never ended")

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

	store := newStoppedByContext(t)
	entered := store.Hang()

	res, err := resolver.New(store, &resolver.Options{ShutdownGrace: grace})
	require.NoError(t, err)

	lookupDone := make(chan error, 1)

	go func() { lookupDone <- res.Contains(context.Background(), []segment.Lookup{lk(segBeta, idAlice)}) }()

	var reached struct{}

	receive(t, entered, &reached, "the lookup never reached the store")

	// the first call has a deadline and leaves while the lookup is still in flight
	first, cancel := context.WithDeadline(context.Background(), farDeadline())
	cancel()

	require.ErrorIs(t, res.Shutdown(first), resolver.ErrShutdown)

	// the second call, with no deadline, drains and reaches the store's Shutdown: the bound is still the first one
	store.Release()

	var lookupErr error

	receive(t, lookupDone, &lookupErr, "the lookup did not end")
	require.NoError(t, lookupErr)

	secondDone := make(chan error, 1)

	go func() { secondDone <- res.Shutdown(context.Background()) }()

	var call storeCall

	receive(t, store.entered, &call, "the second Shutdown never reached the store")
	assert.True(t, call.hasLimit)
	assert.Equal(t, farDeadline().Add(grace), call.deadline)

	store.open()

	var secondErr error

	receive(t, secondDone, &secondErr, "the second Shutdown did not end")
	require.NoError(t, secondErr)
}

// A Shutdown with nothing in flight starts no goroutine of its own: the dump of the goroutines, taken as soon as it
// returns, has none under its label (no wait, no polling).
func TestShutdownWithNothingInFlightLeavesNoGoroutine(t *testing.T) {
	t.Parallel()

	res, err := resolver.New(betaStore(), &resolver.Options{})
	require.NoError(t, err)

	label := "shutdown-idle-" + t.Name() // unique: a goroutine started by Shutdown inherits it

	var shutdownErr error

	pprof.Do(t.Context(), pprof.Labels("test", label), func(ctx context.Context) {
		shutdownErr = res.Shutdown(ctx)
	})

	require.NoError(t, shutdownErr)

	var dump bytes.Buffer

	require.NoError(t, pprof.Lookup("goroutine").WriteTo(&dump, 1))
	assert.NotContains(t, dump.String(), label, "no goroutine of the resolver is left")
}

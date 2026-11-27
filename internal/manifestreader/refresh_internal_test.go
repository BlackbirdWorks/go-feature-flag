package manifestreader

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const refreshBound = 10 * time.Second

// gatedStore answers every request with ErrNotFound, and holds the ones after the first until the test releases them.
type gatedStore struct {
	entered chan struct{}
	release chan struct{}
	gets    atomic.Int64
}

func (s *gatedStore) Get(context.Context, GetRequest) (Object, error) {
	if s.gets.Add(1) > 1 {
		s.entered <- struct{}{}

		<-s.release
	}

	return Object{}, ErrNotFound
}

func startRefreshReader(t *testing.T, store ObjectStore) *Reader {
	t.Helper()

	reader, err := New(store, &Config{ManifestKey: "m", Connection: "c", DisableJitter: true})
	require.NoError(t, err)

	announce := &announcingScheduler{inner: reader.sched, waiting: make(chan struct{}, 8)}
	reader.sched = announce

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)

	go func() { done <- reader.Run(ctx) }()

	t.Cleanup(func() {
		cancel()

		select {
		case runErr := <-done:
			assert.NoError(t, runErr)
		case <-time.After(refreshBound):
			assert.Fail(t, "Run did not stop")
		}
	})

	select {
	case <-announce.waiting:
	case <-time.After(refreshBound):
		require.FailNow(t, "the reader did not end its first round")
	}

	return reader
}

// Calls that arrive while a refresh round runs share it: one request to the store, one result for all.
func TestRefreshCallsShareOneRound(t *testing.T) {
	t.Parallel()

	const callers = 3

	store := &gatedStore{entered: make(chan struct{}, 4), release: make(chan struct{})}
	reader := startRefreshReader(t, store)

	results := make(chan RefreshResult, callers)

	var wg sync.WaitGroup

	for range callers {
		wg.Go(func() {
			result, err := reader.Refresh(t.Context())
			assert.NoError(t, err)

			results <- result
		})
	}

	select {
	case <-store.entered:
	case <-time.After(refreshBound):
		require.FailNow(t, "the refresh round did not start")
	}

	require.Eventually(t, func() bool {
		reader.refresh.mu.Lock()
		defer reader.refresh.mu.Unlock()

		return reader.refresh.current != nil && reader.refresh.current.waiters == callers
	}, refreshBound, time.Millisecond, "the callers joined the round")

	close(store.release)
	wg.Wait()
	close(results)

	for result := range results {
		assert.Equal(t, PollNotFound, result.Poll.Result)
		assert.Equal(t, ReasonMissing, result.Poll.Reason)
		assert.False(t, result.Changed)
		assert.Equal(t, uint64(0), result.Generation)
	}

	assert.Equal(t, int64(2), store.gets.Load(), "the first round and one shared refresh round")
}

// A refresh before Run and after it ended is refused, and a refresh whose context ends stops waiting.
func TestRefreshWhenNotPolling(t *testing.T) {
	t.Parallel()

	reader, err := New(notFoundStore{}, &Config{ManifestKey: "m", Connection: "c", DisableJitter: true})
	require.NoError(t, err)

	_, err = reader.Refresh(t.Context())
	require.ErrorIs(t, err, ErrNotPolling, "before Run")

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)

	go func() { done <- reader.Run(ctx) }()

	cancel()

	select {
	case runErr := <-done:
		require.NoError(t, runErr)
	case <-time.After(refreshBound):
		require.FailNow(t, "Run did not stop")
	}

	_, err = reader.Refresh(t.Context())
	require.ErrorIs(t, err, ErrNotPolling, "after Run")
}

func TestRefreshStopsWaitingWithItsContext(t *testing.T) {
	t.Parallel()

	store := &gatedStore{entered: make(chan struct{}, 4), release: make(chan struct{})}
	reader := startRefreshReader(t, store)

	ctx, cancel := context.WithCancel(t.Context())
	errs := make(chan error, 1)

	go func() {
		_, err := reader.Refresh(ctx)
		errs <- err
	}()

	select {
	case <-store.entered:
	case <-time.After(refreshBound):
		require.FailNow(t, "the refresh round did not start")
	}

	cancel()

	select {
	case err := <-errs:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(refreshBound):
		require.FailNow(t, "Refresh did not stop with its context")
	}

	close(store.release)
}

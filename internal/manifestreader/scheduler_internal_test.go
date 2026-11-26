package manifestreader

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// notFoundStore answers every request with ErrNotFound.
type notFoundStore struct{}

func (notFoundStore) Get(context.Context, GetRequest) (Object, error) { return Object{}, ErrNotFound }

// announcingScheduler tells the test that the reader is about to wait, then waits as the scheduler it wraps.
type announcingScheduler struct {
	inner   Scheduler
	waiting chan struct{}
}

func (a *announcingScheduler) Wait(ctx context.Context, d time.Duration) error {
	a.waiting <- struct{}{}

	return a.inner.Wait(ctx, d)
}

// TestDefaultSchedulerStopsWithTheContext runs the reader with the scheduler New installs, wrapped so that the
// test knows when the wait starts, and cancels only then. A scheduler that ignored its context would keep Run
// waiting for the poll interval, and the bound of the test would fail it.
func TestDefaultSchedulerStopsWithTheContext(t *testing.T) {
	t.Parallel()

	reader, err := New(notFoundStore{}, &Config{ManifestKey: "m", Connection: "c", DisableJitter: true})
	require.NoError(t, err)

	announce := &announcingScheduler{inner: reader.sched, waiting: make(chan struct{}, 1)}
	reader.sched = announce

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	done := make(chan error, 1)

	go func() { done <- reader.Run(ctx) }()

	select {
	case <-announce.waiting:
	case <-time.After(10 * time.Second):
		require.FailNow(t, "the reader did not reach its wait")
	}

	cancel()

	select {
	case runErr := <-done:
		require.NoError(t, runErr)
	case <-time.After(10 * time.Second):
		require.FailNow(t, "Run did not stop with its context: the wait ignores it")
	}

	assert.Equal(t, uint64(1), reader.Status().Polls)
}

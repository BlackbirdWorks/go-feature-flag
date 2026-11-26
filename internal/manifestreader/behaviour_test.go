package manifestreader_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/internal/manifestreader"
)

func TestReader_SnapshotDeliveryIsLatestWins(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	publish(t, store, 1, threeObjects()...)

	rdr := startRig(t, store, &manifestreader.Config{})

	// the consumer never reads: every round must still end
	const generations = 5

	for gen := uint64(2); gen <= generations; gen++ {
		publish(
			t,
			store,
			gen,
			obj{name: nameAlpha, content: fmt.Sprintf(`{"alpha":%d}`, gen)},
			threeObjects()[1],
			threeObjects()[2],
		)
		rdr.round()
	}

	snap := rdr.snapshot()
	require.NotNil(t, snap)
	assert.Equal(t, uint64(generations), snap.Generation, "the older snapshots were replaced")
	assert.Equal(t, `{"alpha":5}`, string(snap.Objects[nameAlpha].Bytes))
	assert.Nil(t, rdr.snapshot(), "nothing else is waiting")
}

func TestReader_SnapshotChannelClosesWhenRunReturns(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	publish(t, store, 1, threeObjects()...)

	rdr := startRig(t, store, &manifestreader.Config{})
	rdr.stop()

	select {
	case snap, ok := <-rdr.reader.Snapshots():
		assert.True(t, ok, "the snapshot not yet read is still delivered")
		assert.Equal(t, uint64(1), snap.Generation)
	case <-time.After(wait):
		require.FailNow(t, "no snapshot")
	}

	select {
	case _, ok := <-rdr.reader.Snapshots():
		assert.False(t, ok)
	case <-time.After(wait):
		require.FailNow(t, "the channel is not closed")
	}
}

func TestReader_RunTwiceIsRefused(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	publish(t, store, 1, threeObjects()...)

	rdr := startRig(t, store, &manifestreader.Config{})

	assert.ErrorIs(t, rdr.reader.Run(t.Context()), manifestreader.ErrAlreadyRunning)
}

func TestReader_FetchConcurrencyIsBounded(t *testing.T) {
	t.Parallel()

	const objects = 24

	tests := []struct {
		name string
		conc int
		want int
	}{
		{name: "one at a time", conc: 1, want: 1},
		{name: "three at a time", conc: 3, want: 3},
		{name: "the default is four", conc: 0, want: manifestreader.DefaultFetchConcurrency},
		{
			name: "sixteen is the most",
			conc: manifestreader.MaxFetchConcurrency,
			want: manifestreader.MaxFetchConcurrency,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Helper()

			t.Parallel()

			objs := make([]obj, objects)
			for i := range objs {
				objs[i] = obj{name: fmt.Sprintf("o%02d", i), content: fmt.Sprintf(`{"n":%d}`, i)}
			}

			store := newFakeStore()
			publish(t, store, 1, objs...)

			entered := make(chan string, objects)
			release := make(chan struct{})
			store.gate = func(ctx context.Context, key string) {
				entered <- key

				select {
				case <-release:
				case <-ctx.Done():
				}
			}

			rdr := launchRig(t, store, &manifestreader.Config{FetchConcurrency: tt.conc, DisableJitter: true})

			// the downloads that can start, start: wait until that many are blocked in the store
			for range tt.want {
				select {
				case <-entered:
				case <-time.After(wait):
					require.FailNow(t, "the downloads did not reach the bound")
				}
			}

			close(release)
			rdr.settle()

			assert.Equal(t, tt.want, store.maxConcurrent(), "never more than the bound, and the bound is used")
			assert.Equal(t, objects, rdr.reader.Status().Counts.OK)
			assert.Len(t, store.takeCalls(), 1+objects)
		})
	}
}

func TestReader_ShutdownDuringAFetchLeavesNothingRunning(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	publish(t, store, 1, threeObjects()...)

	entered := make(chan string, len(threeObjects()))
	store.gate = func(ctx context.Context, key string) {
		entered <- key

		<-ctx.Done()
	}

	rdr := launchRig(t, store, &manifestreader.Config{FetchConcurrency: 2, DisableJitter: true})

	for range 2 {
		select {
		case <-entered:
		case <-time.After(wait):
			require.FailNow(t, "no download started")
		}
	}

	assert.Equal(t, 2, store.active(), "two downloads are blocked in the store")

	rdr.stop()

	assert.Equal(t, 0, store.active(), "every download ended before Run returned")

	select {
	case snap, ok := <-rdr.reader.Snapshots():
		assert.False(t, ok, "no half-done snapshot was published")
		assert.Nil(t, snap)
	case <-time.After(wait):
		require.FailNow(t, "the channel is not closed")
	}
}

// syncBuffer is a log destination safe for the polling goroutine and the test.
type syncBuffer struct {
	buf bytes.Buffer
	mu  sync.Mutex
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

func TestReader_LogsStateChangesOnceAndLeaksNothing(t *testing.T) {
	t.Parallel()

	secret := obj{name: nameAlpha, content: `{"alpha":"SECRET-CONTENT"}`}
	beta, gamma := threeObjects()[1], threeObjects()[2]
	logs := &syncBuffer{}

	store := newFakeStore()
	publish(t, store, 1, threeObjects()...)

	rdr := startRig(t, store, &manifestreader.Config{},
		manifestreader.WithLogger(slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))))
	require.NotNil(t, rdr.snapshot())

	// generation 2: the new alpha fails with an error that carries a secret, for three rounds
	publish(t, store, 2, secret, beta, gamma)
	store.failNext(keyOf(t, secret),
		&manifestreader.FetchError{Err: errBoom, Reason: manifestreader.ReasonError, Retryable: true},
		&manifestreader.FetchError{Err: errBoom, Reason: manifestreader.ReasonError, Retryable: true},
		&manifestreader.FetchError{Err: errBoom, Reason: manifestreader.ReasonError, Retryable: true})

	rdr.round()
	rdr.round()
	rdr.round()

	// the manifest read fails twice, then works
	store.failNext(manifestKey, errBoom, errBoom)
	rdr.round()
	rdr.round()

	// the object recovers
	rdr.round()
	require.NotNil(t, rdr.snapshot())

	out := logs.String()

	assert.Equal(t, 1, strings.Count(out, "object not updated"), "one line per change, not per round")
	assert.Equal(t, 1, strings.Count(out, "object recovered"))
	assert.Equal(t, 1, strings.Count(out, "manifest poll failed"))
	assert.Equal(t, 1, strings.Count(out, "manifest poll recovered"))
	assert.Equal(
		t,
		3,
		strings.Count(out, "snapshot published"),
		"generation 1, generation 2 with a stale object, its recovery",
	)

	assert.Contains(t, out, `"object":"`+nameAlpha+`"`)
	assert.Contains(t, out, `"reason":"error"`)

	for _, forbidden := range []string{
		"SECRET-DETAIL", "SECRET-CONTENT", keyPrefix, "objects/sha256",
		controlplane.ChecksumOf([]byte(secret.content)).Value,
	} {
		assert.NotContains(t, out, forbidden)
		assert.NotContains(t, fmt.Sprintf("%+v", rdr.reader.Status()), forbidden)
	}
}

func TestReader_HooksRestoreSaveAndRound(t *testing.T) {
	t.Parallel()

	alpha, beta, gamma := threeObjects()[0], threeObjects()[1], threeObjects()[2]

	store := newFakeStore()
	manifest := publish(t, store, 1, threeObjects()...)
	raw := mustMarshal(t, manifest)

	type save struct {
		persist manifestreader.Persist
		// requests is the number of requests the store had received when Save was called.
		requests int
	}

	var (
		mu      sync.Mutex
		saved   []save
		rounds  []string
		restore = func() (*manifestreader.Restored, error) {
			return &manifestreader.Restored{
				ETag: etagOf(raw), Manifest: raw,
				// gamma is corrupt: it is not found by its checksum
				Objects: [][]byte{[]byte(alpha.content), []byte(beta.content), []byte(`{"gamma":"corrupt"}`)},
			}, nil
		}
	)

	hooks := manifestreader.Hooks{
		Restore: restore,
		Save: func(p manifestreader.Persist) {
			mu.Lock()
			defer mu.Unlock()

			saved = append(saved, save{persist: p, requests: store.callCount()})
		},
		OnRound: func(status *manifestreader.Status) {
			mu.Lock()
			defer mu.Unlock()

			rounds = append(rounds, status.Poll.Result)
		},
	}

	rdr := startRig(t, store, &manifestreader.Config{}, manifestreader.WithHooks(hooks))

	// the first poll sends the restored ETag (a 304) and only then is the object that was not restored fetched
	assert.Equal(t, sortCalls([]call{manifestCall(etagOf(raw)), objCall(t, gamma)}), store.takeCalls())

	snap := rdr.snapshot()
	require.NotNil(t, snap)
	assert.Equal(t, snapOf(1, map[string]manifestreader.ObjectState{
		nameAlpha: okState(t, alpha), nameBeta: okState(t, beta), nameGamma: okState(t, gamma),
	}), snap)

	mu.Lock()
	defer mu.Unlock()

	require.Len(t, saved, 2, "the restored state, then the state after the fetch")
	assert.Equal(t, 0, saved[0].requests, "the restored objects were served before any request was made")
	assert.Equal(t, 2, saved[1].requests)
	assert.Equal(t, manifestreader.StateMissing, saved[0].persist.Snapshot.Objects[nameGamma].State)
	assert.Equal(t, manifestreader.ReasonNotRestored, saved[0].persist.Snapshot.Objects[nameGamma].Reason)
	assert.Equal(t, manifestreader.StateOK, saved[0].persist.Snapshot.Objects[nameAlpha].State)
	assert.Equal(t, snap, saved[1].persist.Snapshot)

	for _, entry := range saved {
		assert.Equal(t, etagOf(raw), entry.persist.ETag)
		assert.Equal(t, raw, entry.persist.Manifest)
	}

	assert.Equal(t, []string{manifestreader.PollNone, manifestreader.PollNotModified}, rounds)
}

func TestReader_RestoreThatFailsOrHasNothingStartsCold(t *testing.T) {
	t.Parallel()

	tests := []struct {
		restore func() (*manifestreader.Restored, error)
		name    string
	}{
		{name: "an error", restore: func() (*manifestreader.Restored, error) { return nil, errBoom }},
		{name: "nothing saved", restore: func() (*manifestreader.Restored, error) {
			return nil, manifestreader.ErrNothingRestored
		}},
		{name: "a saved manifest that is not valid", restore: func() (*manifestreader.Restored, error) {
			return &manifestreader.Restored{ETag: `"x"`, Manifest: []byte("garbage: [")}, nil
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Helper()

			t.Parallel()

			store := newFakeStore()
			publish(t, store, 1, threeObjects()...)

			rdr := startRig(
				t,
				store,
				&manifestreader.Config{},
				manifestreader.WithHooks(manifestreader.Hooks{Restore: tt.restore}),
			)

			calls := store.takeCalls()
			require.NotEmpty(t, calls)
			assert.Equal(t, manifestKey, calls[0].Key)
			assert.NotNil(t, rdr.snapshot())
			assert.Equal(t, accepted(1, 3), rdr.reader.Status().Manifest)
		})
	}
}

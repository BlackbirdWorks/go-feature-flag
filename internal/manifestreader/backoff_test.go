package manifestreader_test

import (
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/manifestreader"
)

func sec(n int) time.Duration { return time.Duration(n) * time.Second }

func throttle() error {
	return &manifestreader.FetchError{
		Err: errTransient, Reason: manifestreader.ReasonError, Retryable: true, Throttled: true,
	}
}

func fixedJitter(value float64) manifestreader.Option {
	return manifestreader.WithJitter(func() float64 { return value })
}

// TestReader_ManifestPollBackoff asserts the exact waits the reader asks the scheduler for after a sequence of
// manifest polls. The first wait is the one after the cold start round. A failed poll waits a full jitter draw
// in [0, min(cap, interval x 2^n)) (n + 1 for a throttle), never less than the regular wait; a 200 or a 304
// resets it.
func TestReader_ManifestPollBackoff(t *testing.T) {
	t.Parallel()

	var (
		ok       error
		failed   = errTransient
		notFound = manifestreader.ErrNotFound
	)

	tests := []struct {
		name        string
		steps       []error
		wantWaits   []time.Duration
		cfg         manifestreader.Config
		wantBackoff manifestreader.BackoffStatus
		jitter      float64
		garbage     bool
	}{
		{
			name:   "errors double up to the cap, and the first success resets",
			jitter: 0.75, steps: []error{failed, failed, failed, failed, ok, ok},
			// ceilings 120, 240, 300, 300 times 0.75
			wantWaits:   []time.Duration{sec(60), sec(90), sec(180), sec(225), sec(225), sec(60), sec(60)},
			wantBackoff: manifestreader.BackoffStatus{Wait: sec(60)},
		},
		{
			name:   "a failure never makes the reader poll faster than the interval",
			jitter: 0.1, steps: []error{failed, failed},
			// 12 s and 24 s are below the 60 s interval
			wantWaits:   []time.Duration{sec(60), sec(60), sec(60)},
			wantBackoff: manifestreader.BackoffStatus{Failures: 2, Wait: sec(60)},
		},
		{
			name:   "a throttle backs off one step further than an error",
			jitter: 0.5, steps: []error{throttle(), throttle(), throttle()},
			// ceilings 240, 300 (480 capped), 300, times 0.5
			wantWaits:   []time.Duration{sec(60), sec(120), sec(150), sec(150)},
			wantBackoff: manifestreader.BackoffStatus{Failures: 3, Wait: sec(150), Throttled: true},
		},
		{
			name:   "the same failure as a plain error waits less than as a throttle",
			jitter: 0.5, steps: []error{failed, failed, failed},
			// ceilings 120, 240, 300, times 0.5: 60 (the interval), 120, 150
			wantWaits:   []time.Duration{sec(60), sec(60), sec(120), sec(150)},
			wantBackoff: manifestreader.BackoffStatus{Failures: 3, Wait: sec(150)},
		},
		{
			name:   "a missing manifest is a failure of the poll",
			jitter: 0.75, steps: []error{notFound, notFound},
			wantWaits:   []time.Duration{sec(60), sec(90), sec(180)},
			wantBackoff: manifestreader.BackoffStatus{Failures: 2, Wait: sec(180)},
		},
		{
			name:   "a throttle after errors continues the count",
			jitter: 0.5, steps: []error{failed, failed, throttle()},
			// n=1: 120 x 0.5 -> 60; n=2: 240 x 0.5 = 120; n=3 throttled: 960 capped 300 x 0.5 = 150
			wantWaits:   []time.Duration{sec(60), sec(60), sec(120), sec(150)},
			wantBackoff: manifestreader.BackoffStatus{Failures: 3, Wait: sec(150), Throttled: true},
		},
		{
			name:   "a manifest that was read and rejected is not a failure of the poll",
			jitter: 0.75, steps: []error{failed, ok}, garbage: true,
			wantWaits:   []time.Duration{sec(60), sec(90), sec(60)},
			wantBackoff: manifestreader.BackoffStatus{Wait: sec(60)},
		},
		{
			name:   "jitter 0 draws the shortest wait: the interval",
			jitter: 0, steps: []error{failed, failed, failed},
			wantWaits:   []time.Duration{sec(60), sec(60), sec(60), sec(60)},
			wantBackoff: manifestreader.BackoffStatus{Failures: 3, Wait: sec(60)},
		},
		{
			name:   "a cap below the interval is raised to it",
			cfg:    manifestreader.Config{PollInterval: sec(120), BackoffMax: sec(30)},
			jitter: 0.75, steps: []error{failed},
			// ceiling min(240, 120) x 0.75 = 90, below the 120 s interval
			wantWaits:   []time.Duration{sec(120), sec(120)},
			wantBackoff: manifestreader.BackoffStatus{Failures: 1, Wait: sec(120)},
		},
		{
			name:   "a configured cap",
			cfg:    manifestreader.Config{BackoffMax: sec(100)},
			jitter: 0.9, steps: []error{failed, failed, failed},
			// ceilings 100, 100, 100 times 0.9 = 90
			wantWaits:   []time.Duration{sec(60), sec(90), sec(90), sec(90)},
			wantBackoff: manifestreader.BackoffStatus{Failures: 3, Wait: sec(90)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newFakeStore()
			publish(t, store, 1, threeObjects()...)

			cfg := tt.cfg
			rdr := startRig(t, store, &cfg, fixedJitter(tt.jitter))
			require.Equal(t, []time.Duration{rdr.reader.Status().Backoff.Wait}, rdr.sched.waited())

			for _, step := range tt.steps {
				if step != nil {
					store.failNext(manifestKey, step)
				}

				if tt.garbage && step == nil {
					store.put(manifestKey, []byte("not a manifest"))
				}

				rdr.round()
			}

			assert.Equal(t, tt.wantWaits, rdr.sched.waited())
			assert.Equal(t, tt.wantBackoff, withoutWaiting(rdr.reader.Status().Backoff))
		})
	}
}

func withoutWaiting(status manifestreader.BackoffStatus) manifestreader.BackoffStatus {
	status.ObjectsWaiting = 0

	return status
}

// TestReader_FailingObjectBackoff counts the object GETs over rounds of a permanently failing object: it is
// retried with a growing, capped interval, not at every round. A non-retryable failure is retried at the slow
// fixed cadence of the cap. A new generation tries it at once.
func TestReader_FailingObjectBackoff(t *testing.T) {
	t.Parallel()

	denied := &manifestreader.FetchError{Err: errDenied, Reason: manifestreader.ReasonDenied}

	tests := []struct {
		fail       func(*fakeStore, string)
		name       string
		wantRounds []int
		jitter     float64
		rounds     int
	}{
		{
			// full jitter 0.5: after the failure n the next try is vnow + 0.5 x min(300 s, 60 s x 2^(n-1))
			name: "a missing object: 30 s, 60 s, 120 s, then the cap of 150 s", jitter: 0.5, rounds: 11,
			fail:       func(store *fakeStore, key string) { store.remove(key) },
			wantRounds: []int{0, 1, 2, 4, 7, 10},
		},
		{
			name:   "jitter 0: no wait, the object is tried at every round (the budget of a healthy publisher)",
			jitter: 0, rounds: 5,
			fail:       func(store *fakeStore, key string) { store.remove(key) },
			wantRounds: []int{0, 1, 2, 3, 4},
		},
		{
			name: "a denied object is tried every 5 minutes", jitter: 0.5, rounds: 11,
			fail: func(store *fakeStore, key string) {
				store.failNext(key, slices.Repeat([]error{denied}, 100)...)
			},
			wantRounds: []int{0, 5, 10},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newFakeStore()
			objs := threeObjects()
			publish(t, store, 1, objs...)
			tt.fail(store, keyOf(t, objs[0]))

			// round 0 is the cold start round
			rdr := startRig(t, store, &manifestreader.Config{}, fixedJitter(tt.jitter))

			var got []int

			// the cold start round already ran: its calls are the first in the store
			tried := func(calls []call, round int) {
				for _, c := range calls {
					if c.Key == keyOf(t, objs[0]) {
						got = append(got, round)
					}
				}
			}

			tried(store.takeCalls(), 0)

			for round := 1; round < tt.rounds; round++ {
				tried(rdr.round(), round)
			}

			assert.Equal(t, tt.wantRounds, got)

			status := rdr.reader.Status()
			require.Len(t, status.Failures, 1)
			assert.Equal(t, objs[0].name, status.Failures[0].Name)
		})
	}
}

func TestReader_FailingObjectIsTriedAtOnceByANewGeneration(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	objs := threeObjects()
	publish(t, store, 1, objs...)
	store.remove(keyOf(t, objs[0]))

	rdr := startRig(t, store, &manifestreader.Config{DisableFetchSpread: true}, fixedJitter(0.9))
	store.takeCalls()

	// the object keeps failing and is backed off: a round costs the manifest GET only
	etag := manifestETag(store)

	rdr.round()
	assert.Equal(t, 1, rdr.reader.Status().Backoff.ObjectsWaiting)
	assert.Equal(t, condGet(t, etag), rdr.round())

	// a new generation lists the same object again: it is tried in that very round
	publish(t, store, 2, objs...)
	store.remove(keyOf(t, objs[0]))

	assert.Equal(t, []call{manifestCall(etag), objCall(t, objs[0])}, rdr.round())

	// the publisher repairs the object; the backoff still applies until the next try is due, and then it is fetched
	store.put(keyOf(t, objs[0]), []byte(objs[0].content))

	var recovered bool

	for range 12 {
		for _, c := range rdr.round() {
			recovered = recovered || c.Key == keyOf(t, objs[0])
		}

		if recovered {
			break
		}
	}

	require.True(t, recovered, "the object is fetched within the backoff cap")
	assert.Empty(t, rdr.reader.Status().Failures)
	assert.Zero(t, rdr.reader.Status().Backoff.ObjectsWaiting)
}

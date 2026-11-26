package manifestreader_test

import (
	"bytes"
	"context"
	"log/slog"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/internal/manifestreader"
)

func objectsContent(objs []obj) [][]byte {
	out := make([][]byte, len(objs))
	for i := range objs {
		out[i] = []byte(objs[i].content)
	}

	return out
}

func TestReader_RestoredGarbageWithTheLiveETagStartsWithAnUnconditionalGet(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	publish(t, store, 1, threeObjects()...)

	hooks := manifestreader.Hooks{Restore: func() (*manifestreader.Restored, error) {
		// the saved manifest is corrupt, and the saved ETag is the one the live manifest has
		return &manifestreader.Restored{ETag: manifestETag(store), Manifest: []byte("{truncated: [")}, nil
	}}

	rdr := startRig(t, store, &manifestreader.Config{}, manifestreader.WithHooks(hooks))

	calls := store.takeCalls()
	require.NotEmpty(t, calls)
	assert.Equal(t, manifestCall(""), calls[0], "no If-None-Match: a 304 would have kept the reader empty for ever")
	assert.Len(t, calls, 4)

	snap := rdr.snapshot()
	require.NotNil(t, snap)
	assert.Equal(t, uint64(1), snap.Generation)
	assert.Equal(t, accepted(1, 3), rdr.reader.Status().Manifest)
}

func TestReader_RestoredManifestThatIsRejectedClearsTheETag(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	publish(t, store, 2, threeObjects()...)

	// a saved manifest of a channel the reader does not serve
	other := manifestOf(t, 1, threeObjects()...)
	other.Channel = otherChannel
	raw := mustMarshal(t, other)

	hooks := manifestreader.Hooks{Restore: func() (*manifestreader.Restored, error) {
		return &manifestreader.Restored{ETag: manifestETag(store), Manifest: raw}, nil
	}}

	rdr := startRig(t, store, &manifestreader.Config{}, manifestreader.WithHooks(hooks))

	assert.Equal(t, manifestCall(""), store.takeCalls()[0])
	assert.NotNil(t, rdr.snapshot())
}

func TestReader_SaveGetsTheETagOfTheAcceptedManifest(t *testing.T) {
	t.Parallel()

	alpha, beta, gamma := threeObjects()[0], threeObjects()[1], threeObjects()[2]

	var (
		mu    sync.Mutex
		saved []manifestreader.Persist
	)

	hooks := manifestreader.Hooks{Save: func(p manifestreader.Persist) {
		mu.Lock()
		defer mu.Unlock()

		saved = append(saved, p)
	}}

	// generation 1 (A) is accepted with alpha not written yet
	store := newFakeStore()
	store.put(keyOf(t, beta), []byte(beta.content))
	store.put(keyOf(t, gamma), []byte(gamma.content))
	putManifest(t, store, manifestOf(t, 1, alpha, beta, gamma))

	rawA := store.objects[manifestKey]
	etagA := manifestETag(store)

	rdr := startRig(t, store, &manifestreader.Config{}, manifestreader.WithHooks(hooks))
	require.NotNil(t, rdr.snapshot())

	// B is read and rejected
	store.put(manifestKey, []byte("not: [a manifest"))
	etagB := manifestETag(store)

	assert.Equal(t, []call{manifestCall(etagA)}, rdr.round())

	// alpha appears: the retry runs on a 304 round against B's ETag
	store.put(keyOf(t, alpha), []byte(alpha.content))
	assert.Equal(t, []call{manifestCall(etagB), objCall(t, alpha)}, rdr.round())
	require.NotNil(t, rdr.snapshot())

	mu.Lock()
	require.Len(t, saved, 2)

	last := saved[1]
	mu.Unlock()

	assert.Equal(t, etagA, last.ETag, "the ETag of the accepted manifest, not of the rejected one")
	assert.Equal(t, rawA, last.Manifest)

	// a restart from what was saved: the first poll sends A's ETag, reads B, rejects it, keeps what it serves
	restart := manifestreader.Hooks{Restore: func() (*manifestreader.Restored, error) {
		return &manifestreader.Restored{
			ETag:     last.ETag,
			Manifest: last.Manifest,
			Objects:  objectsContent(threeObjects()),
		}, nil
	}}

	second := startRig(t, store, &manifestreader.Config{}, manifestreader.WithHooks(restart))

	assert.Equal(t, []call{manifestCall(etagA)}, store.takeCalls())
	assert.Equal(t, manifestreader.OutcomeInvalid, second.reader.Status().Manifest.Outcome)
	assert.Equal(t, uint64(1), second.reader.Status().Manifest.Generation)

	snap := second.snapshot()
	require.NotNil(t, snap, "the restored snapshot is served")
	assert.Equal(t, manifestreader.StateOK, snap.Objects[nameAlpha].State)

	// the publisher corrects the manifest: B's ETag was kept, so the new bytes are read
	alpha2 := obj{name: nameAlpha, content: contentAlphaChanged}
	publish(t, store, 2, alpha2, beta, gamma)

	assert.Equal(t, []call{manifestCall(etagB), objCall(t, alpha2)}, second.round())
	assert.Equal(t, uint64(2), second.snapshot().Generation)
}

// timeoutSeam replaces the creation of the request contexts: the test ends a request itself.
type timeoutSeam struct {
	cancels   chan context.CancelFunc
	durations []time.Duration
	mu        sync.Mutex
}

func newTimeoutSeam() *timeoutSeam { return &timeoutSeam{cancels: make(chan context.CancelFunc, 64)} }

func (s *timeoutSeam) create(parent context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)

	s.mu.Lock()
	s.durations = append(s.durations, d)
	s.mu.Unlock()

	s.cancels <- cancel

	return ctx, cancel
}

func (s *timeoutSeam) next(t *testing.T) context.CancelFunc {
	t.Helper()

	select {
	case cancel := <-s.cancels:
		return cancel
	case <-time.After(wait):
		require.FailNow(t, "no request context was created")
	}

	return nil
}

func (s *timeoutSeam) seen() []time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]time.Duration(nil), s.durations...)
}

func awaitHung(t *testing.T, store *fakeStore, want string) {
	t.Helper()

	select {
	case key := <-store.hung:
		require.Equal(t, want, key)
	case <-time.After(wait):
		require.FailNow(t, "the request did not block")
	}
}

func TestReader_ManifestRequestTimeout(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	publish(t, store, 1, threeObjects()...)
	store.setHang(manifestKey, true)

	seam := newTimeoutSeam()
	rdr := launchRig(t, store, &manifestreader.Config{RequestTimeout: 7 * time.Second, DisableJitter: true},
		manifestreader.WithRequestContext(seam.create))

	awaitHung(t, store, manifestKey)
	seam.next(t)() // the timeout fires

	rdr.settle() // the round ended: the reader is waiting for the next tick

	status := rdr.reader.Status()
	assert.Equal(t, manifestreader.PollError, status.Poll.Result)
	assert.Equal(t, manifestreader.ReasonTimeout, status.Poll.Reason)
	assert.Nil(t, rdr.snapshot())

	// the next round runs
	store.setHang(manifestKey, false)
	rdr.round()

	assert.NotNil(t, rdr.snapshot())
	assert.Empty(t, rdr.reader.Status().Poll.Reason)

	for _, d := range seam.seen() {
		assert.Equal(t, 7*time.Second, d, "every request carries the configured timeout")
	}

	assert.Len(t, seam.seen(), 5, "the manifest twice and the three objects")
}

func TestReader_ObjectRequestTimeout(t *testing.T) {
	t.Parallel()

	alpha := threeObjects()[0]
	store := newFakeStore()
	publish(t, store, 1, threeObjects()...)
	store.setHang(keyOf(t, alpha), true)

	seam := newTimeoutSeam()
	rdr := launchRig(t, store, &manifestreader.Config{FetchConcurrency: 1, DisableJitter: true},
		manifestreader.WithRequestContext(seam.create))

	awaitHung(t, store, keyOf(t, alpha))
	seam.next(t)() // the manifest context, already finished
	seam.next(t)() // the context of alpha: the timeout fires

	rdr.settle()

	snap := rdr.snapshot()
	require.NotNil(t, snap)
	assert.Equal(t, manifestreader.StateMissing, snap.Objects[nameAlpha].State)
	assert.Equal(t, manifestreader.ReasonTimeout, snap.Objects[nameAlpha].Reason)
	assert.Equal(t, manifestreader.StateOK, snap.Objects[nameBeta].State, "the others are not held back")
	assert.Equal(t, []manifestreader.ObjectFailure{{
		Name: nameAlpha, State: manifestreader.StateMissing, Reason: manifestreader.ReasonTimeout,
		Checksum: controlplane.ChecksumOf([]byte(alpha.content)).Value, Retryable: true,
	}}, rdr.reader.Status().Failures)

	store.setHang(keyOf(t, alpha), false)
	assert.Equal(t, []call{manifestCall(manifestETag(store)), objCall(t, alpha)}, rdr.round())
	assert.Equal(t, manifestreader.StateOK, rdr.snapshot().Objects[nameAlpha].State)
	assert.Equal(t, 60*time.Second, seam.seen()[0], "the default timeout")
}

func TestReader_TotalBytesLimit(t *testing.T) {
	t.Parallel()

	alpha := threeObjects()[0] // 11 bytes
	twin := obj{name: nameTwin, content: alpha.content}

	tests := []struct {
		name         string
		objs         []obj
		max          int64
		wantCalls    int
		wantAccepted bool
	}{
		{
			name:         "the sum of three objects is 32: at the limit",
			max:          32,
			objs:         threeObjects(),
			wantAccepted: true,
			wantCalls:    4,
		},
		{
			name:      "one byte over the limit is rejected whole, with no object request",
			max:       31,
			objs:      threeObjects(),
			wantCalls: 1,
		},
		{
			name:         "a checksum listed twice counts once",
			max:          11,
			objs:         []obj{alpha, twin},
			wantAccepted: true,
			wantCalls:    2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newFakeStore()
			publish(t, store, 1, tt.objs...)

			rdr := startRig(t, store, &manifestreader.Config{MaxTotalBytes: tt.max})

			assert.Len(t, store.takeCalls(), tt.wantCalls)
			assert.Equal(t, tt.wantAccepted, rdr.snapshot() != nil)

			status := rdr.reader.Status()
			if !tt.wantAccepted {
				assert.Equal(t, manifestreader.OutcomeInvalid, status.Manifest.Outcome)
				assert.Equal(t, manifestreader.ReasonTotalTooLarge, status.Manifest.Reason)
			}
		})
	}
}

func TestReader_EqualGenerationIdentity(t *testing.T) {
	t.Parallel()

	rewrite := func(mutate func(m *controlplane.Manifest)) func(*testing.T, *fakeStore) {
		return func(t *testing.T, store *fakeStore) {
			t.Helper()

			manifest := manifestOf(t, 1, threeObjects()...)
			mutate(manifest)
			putManifest(t, store, manifest)
		}
	}

	tests := []struct {
		name         string
		mutate       func(m *controlplane.Manifest)
		wantOutcome  string
		wantSnapshot bool
	}{
		{
			name:        "another kind is a conflict",
			mutate:      func(m *controlplane.Manifest) { m.Objects[1].Kind = controlplane.KindFlagSets },
			wantOutcome: manifestreader.OutcomeConflict,
		},
		{
			name:        "another format is a conflict",
			mutate:      func(m *controlplane.Manifest) { m.Objects[1].Format = controlplane.FormatYAML },
			wantOutcome: manifestreader.OutcomeConflict,
		},
		{
			name:        "another key with the same checksum is accepted and changes nothing",
			mutate:      func(m *controlplane.Manifest) { m.Objects[1].Key = "objects/moved/beta.json" },
			wantOutcome: manifestreader.OutcomeAccepted,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			runSteps(t, []step{{
				name:      tt.name,
				act:       rewrite(tt.mutate),
				wantCalls: condGet,
				check: func(t *testing.T, status manifestreader.Status) {
					t.Helper()

					assert.Equal(t, tt.wantOutcome, status.Manifest.Outcome)
					assert.Equal(t, uint64(1), status.Manifest.Generation)
					assert.Equal(t, manifestreader.Counts{OK: 3}, status.Counts)
				},
			}})
		})
	}
}

func TestReader_SizeIsBinding(t *testing.T) {
	t.Parallel()

	alpha, beta, gamma := threeObjects()[0], threeObjects()[1], threeObjects()[2]

	tests := []struct {
		name         string
		wantReason   string
		size         int64
		maxObject    int64
		wantRequests int
	}{
		{name: "size 0 is invalid and costs no request", size: 0, wantReason: manifestreader.ReasonInvalidSize},
		{
			name:       "a size above the object limit costs no request",
			size:       20,
			maxObject:  15,
			wantReason: manifestreader.ReasonTooLarge,
		},
		{
			name: "a body shorter than the size is a size mismatch", size: 20,
			wantReason: manifestreader.ReasonSizeMismatch, wantRequests: 1,
		},
		{
			name: "a body longer than the size is a size mismatch", size: 5,
			wantReason: manifestreader.ReasonSizeMismatch, wantRequests: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newFakeStore()
			store.put(keyOf(t, alpha), []byte(alpha.content))
			store.put(keyOf(t, beta), []byte(beta.content))
			store.put(keyOf(t, gamma), []byte(gamma.content))

			manifest := manifestOf(t, 1, alpha, beta, gamma)
			manifest.Objects[0].Size = tt.size
			putManifest(t, store, manifest)

			rdr := startRig(t, store, &manifestreader.Config{MaxObjectBytes: tt.maxObject})

			snap := rdr.snapshot()
			require.NotNil(t, snap)
			assert.Equal(t, manifestreader.StateMissing, snap.Objects[nameAlpha].State)
			assert.Equal(t, tt.wantReason, snap.Objects[nameAlpha].Reason)
			assert.NotEqual(t, manifestreader.ReasonChecksumMismatch, snap.Objects[nameAlpha].Reason)
			assert.Equal(t, manifestreader.StateOK, snap.Objects[nameBeta].State)

			alphaRequests := 0

			for _, c := range store.takeCalls() {
				if c.Key == keyOf(t, alpha) {
					alphaRequests++
				}
			}

			assert.Equal(t, tt.wantRequests, alphaRequests, "the cold start")

			// every later round: the manifest, and alpha again only when a request was ever made for it
			for range 3 {
				want := []call{manifestCall(manifestETag(store))}
				for range tt.wantRequests {
					want = append(want, objCall(t, alpha))
				}

				assert.Equal(t, want, rdr.round())
			}
		})
	}
}

func TestReader_ObjectsAreNotRetriedWhenTheManifestCannotBeRead(t *testing.T) {
	t.Parallel()

	alpha, beta, gamma := threeObjects()[0], threeObjects()[1], threeObjects()[2]

	store := newFakeStore()
	store.put(keyOf(t, beta), []byte(beta.content))
	store.put(keyOf(t, gamma), []byte(gamma.content))
	putManifest(t, store, manifestOf(t, 1, alpha, beta, gamma)) // alpha is not written: it fails

	rdr := startRig(t, store, &manifestreader.Config{})
	require.NotNil(t, rdr.snapshot())

	etag := manifestETag(store)
	store.failNext(
		manifestKey,
		&manifestreader.FetchError{Err: errBoom, Reason: manifestreader.ReasonError, Retryable: true},
	)

	assert.Equal(t, []call{manifestCall(etag)}, rdr.round(), "an outage adds no object request to the failing one")
	assert.Equal(
		t,
		[]call{manifestCall(etag), objCall(t, alpha)},
		rdr.round(),
		"and the retry resumes with the manifest",
	)
}

func TestReader_SameChecksumUnderSeveralEntries(t *testing.T) {
	t.Parallel()

	alpha := threeObjects()[0]
	twin := obj{name: nameTwin, content: alpha.content}
	altKey := "objects/alt/twin.json"

	build := func(t *testing.T, mutate func(m *controlplane.Manifest)) (*fakeStore, *controlplane.Manifest) {
		t.Helper()

		manifest := manifestOf(t, 1, alpha, twin)
		manifest.Objects[1].Key = altKey
		mutate(manifest)

		store := newFakeStore()
		putManifest(t, store, manifest)

		return store, manifest
	}

	tests := []struct {
		mutate     func(m *controlplane.Manifest)
		prepare    func(t *testing.T, store *fakeStore)
		name       string
		wantReason string
		wantCalls  []call
		wantOK     bool
	}{
		{
			name:    "the first key is bad, the second is good: both names are ok",
			mutate:  func(*controlplane.Manifest) {},
			prepare: func(_ *testing.T, store *fakeStore) { store.put(altKey, []byte(alpha.content)) },
			wantCalls: []call{
				manifestCall(""), {Key: altKey}, objCall(t, alpha),
			},
			wantOK: true,
		},
		{
			name:       "all entries fail: both names get the same failure, one request per entry",
			mutate:     func(*controlplane.Manifest) {},
			prepare:    func(*testing.T, *fakeStore) {},
			wantCalls:  []call{manifestCall(""), {Key: altKey}, objCall(t, alpha)},
			wantReason: manifestreader.ReasonMissing,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store, _ := build(t, tt.mutate)
			tt.prepare(t, store)

			rdr := startRig(t, store, &manifestreader.Config{})

			assert.Equal(t, sortCalls(tt.wantCalls), store.takeCalls())

			snap := rdr.snapshot()
			require.NotNil(t, snap)

			for _, name := range []string{nameAlpha, nameTwin} {
				if tt.wantOK {
					assert.Equal(t, manifestreader.StateOK, snap.Objects[name].State, name)
				} else {
					assert.Equal(t, manifestreader.StateMissing, snap.Objects[name].State, name)
					assert.Equal(t, tt.wantReason, snap.Objects[name].Reason, name)
				}
			}

			if tt.wantOK {
				assert.Equal(t, []call{manifestCall(manifestETag(store))}, rdr.round(), "nothing is fetched again")

				return
			}

			// a failed group is tried again on the next round, each entry once
			assert.Equal(
				t,
				sortCalls([]call{manifestCall(manifestETag(store)), {Key: altKey}, objCall(t, alpha)}),
				rdr.round(),
			)
		})
	}
}

func TestReader_VersionIDIsPassedAndConnectionsAreChecked(t *testing.T) {
	t.Parallel()

	alpha, beta, gamma := threeObjects()[0], threeObjects()[1], threeObjects()[2]

	store := newFakeStore()
	manifest := publish(t, store, 1, alpha, beta, gamma)
	manifest.Objects[0].VersionID = "v-123"
	manifest.Objects[1].Connection = connElsewhere
	putManifest(t, store, manifest)

	rdr := startRig(t, store, &manifestreader.Config{})

	assert.Equal(t, sortCalls([]call{
		manifestCall(""), {Key: keyOf(t, alpha), VersionID: "v-123"}, objCall(t, gamma),
	}), store.takeCalls(), "the version is passed; the other connection is not read from this store")

	snap := rdr.snapshot()
	require.NotNil(t, snap)
	assert.Equal(t, manifestreader.ReasonUnknownConnection, snap.Objects[nameBeta].Reason)
	assert.Equal(t, manifestreader.StateOK, snap.Objects[nameAlpha].State)

	// an entry that can never be requested costs nothing in later rounds
	assert.Equal(t, []call{manifestCall(manifestETag(store))}, rdr.round())
}

func TestReader_HookPanicsAreRecoveredAndReported(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	publish(t, store, 1, threeObjects()...)

	logs := &syncBuffer{}
	panics := manifestreader.Hooks{
		Restore: func() (*manifestreader.Restored, error) { panic("restore boom") },
		Save:    func(manifestreader.Persist) { panic("save boom") },
		OnRound: func(*manifestreader.Status) { panic("round boom") },
	}

	rdr := startRig(t, store, &manifestreader.Config{},
		manifestreader.WithHooks(panics),
		manifestreader.WithLogger(slog.New(slog.NewJSONHandler(logs, nil))))

	require.NotNil(t, rdr.snapshot(), "polling and delivery go on")

	// extra rounds panic again in OnRound: polling still goes on
	alpha2 := obj{name: nameAlpha, content: contentAlphaChanged}
	publish(t, store, 2, alpha2, threeObjects()[1], threeObjects()[2])
	rdr.round()
	require.NotNil(t, rdr.snapshot())
	rdr.round()

	status := rdr.reader.Status()
	assert.Equal(t, uint64(3), status.Polls)
	assert.Equal(t, []manifestreader.HookPanic{
		{Name: "onRound", Count: 2}, // the panic of the round in progress shows in the next status
		{Name: "restore", Count: 1},
		{Name: "save", Count: 2},
	}, status.HookPanics)

	out := logs.String()
	for _, hook := range []string{"restore", "save", "onRound"} {
		assert.Equal(t, 1, strings.Count(out, `"hook":"`+hook+`"`), "logged once: %s", hook)
	}

	assert.NotContains(t, out, "boom", "the panic value is not logged")
}

func TestReader_SaveGetsTheSnapshotItPublished(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	manifest := publish(t, store, 1, threeObjects()...)
	raw := mustMarshal(t, manifest)

	var persisted manifestreader.Persist

	rdr := startRig(t, store, &manifestreader.Config{},
		manifestreader.WithHooks(manifestreader.Hooks{Save: func(p manifestreader.Persist) { persisted = p }}))

	assert.True(t, bytes.Equal(raw, persisted.Manifest))
	assert.Same(t, rdr.snapshot(), persisted.Snapshot)
}

func TestReader_KeysAreAbsoluteBucketKeys(t *testing.T) {
	t.Parallel()

	alpha, beta, gamma := threeObjects()[0], threeObjects()[1], threeObjects()[2]

	tests := []struct {
		name string
		key  string
		// wantKey is the key requested for alpha, or empty when no request is made for it.
		wantKey string
		wantOK  bool
	}{
		{
			name: "a key with a prefix path is requested verbatim",
			key:  keyPrefix + "deep/alpha.json", wantKey: keyPrefix + "deep/alpha.json", wantOK: true,
		},
		{name: "a key with a leading slash costs no request", key: "/alpha.json"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newFakeStore()
			store.put(tt.key, []byte(alpha.content))
			store.put(keyOf(t, beta), []byte(beta.content))
			store.put(keyOf(t, gamma), []byte(gamma.content))

			manifest := manifestOf(t, 1, alpha, beta, gamma)
			manifest.Objects[0].Key = tt.key
			putManifest(t, store, manifest)

			rdr := startRig(t, store, &manifestreader.Config{})

			snap := rdr.snapshot()
			require.NotNil(t, snap)

			wantCalls := []call{manifestCall(""), objCall(t, beta), objCall(t, gamma)}
			if tt.wantOK {
				wantCalls = append(wantCalls, call{Key: tt.wantKey})

				assert.Equal(t, manifestreader.StateOK, snap.Objects[nameAlpha].State)
			} else {
				assert.Equal(t, manifestreader.StateMissing, snap.Objects[nameAlpha].State)
				assert.Equal(t, manifestreader.ReasonInvalidKey, snap.Objects[nameAlpha].Reason)
			}

			assert.Equal(t, sortCalls(wantCalls), store.takeCalls())

			// later rounds: a bad key stays free of requests
			for range 2 {
				assert.Equal(t, []call{manifestCall(manifestETag(store))}, rdr.round())
			}
		})
	}
}

func TestReader_EmptyKeyRejectsTheManifestWithoutObjectRequests(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	manifest := publish(t, store, 1, threeObjects()...)
	manifest.Objects[0].Key = ""
	putManifest(t, store, manifest)

	rdr := startRig(t, store, &manifestreader.Config{})

	assert.Equal(t, []call{manifestCall("")}, store.takeCalls())
	assert.Nil(t, rdr.snapshot())
	assert.Equal(t, string(controlplane.CodeMissingValue), rdr.reader.Status().Manifest.Reason)
}

func TestReader_TotalBytesDoesNotDependOnTheOrderOfEntries(t *testing.T) {
	t.Parallel()

	alpha, beta := threeObjects()[0], threeObjects()[1] // 11 and 10 bytes
	twin := obj{name: nameTwin, content: alpha.content}

	tests := []struct {
		name       string
		wantReason string
		wantCalls  []call
		sizes      [2]int64
	}{
		{
			name: "the small size first", sizes: [2]int64{5, 11},
			wantCalls: []call{manifestCall("")}, wantReason: manifestreader.ReasonTotalTooLarge,
		},
		{
			name: "the large size first", sizes: [2]int64{11, 5},
			wantCalls: []call{manifestCall("")}, wantReason: manifestreader.ReasonTotalTooLarge,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newFakeStore()
			manifest := manifestOf(t, 1, alpha, twin, beta)
			manifest.Objects[0].Size, manifest.Objects[1].Size = tt.sizes[0], tt.sizes[1]

			store.put(keyOf(t, alpha), []byte(alpha.content))
			store.put(keyOf(t, beta), []byte(beta.content))
			putManifest(t, store, manifest)

			rdr := startRig(t, store, &manifestreader.Config{MaxTotalBytes: 20})

			assert.Equal(t, tt.wantCalls, store.takeCalls())
			assert.Nil(t, rdr.snapshot())
			assert.Equal(t, tt.wantReason, rdr.reader.Status().Manifest.Reason)
		})
	}
}

func TestReader_TotalBytesCannotOverflow(t *testing.T) {
	t.Parallel()

	alpha, beta := threeObjects()[0], threeObjects()[1]

	tests := []struct {
		name         string
		wantReason   string
		cfg          manifestreader.Config
		sizes        [2]int64
		wantCalls    int
		wantAccepted bool
	}{
		{
			name:         "a size above the object limit counts for nothing: the huge entry is not requested, the small one is",
			sizes:        [2]int64{math.MaxInt64, 10},
			wantAccepted: true,
			wantReason:   manifestreader.ReasonTooLarge,
			wantCalls:    2,
		},
		{
			name:  "with a huge object limit the two sizes would wrap the sum: the manifest is rejected",
			cfg:   manifestreader.Config{MaxObjectBytes: math.MaxInt64, MaxTotalBytes: math.MaxInt64},
			sizes: [2]int64{math.MaxInt64, 1}, wantReason: manifestreader.ReasonTotalTooLarge, wantCalls: 1,
		},
		{
			name:  "exactly the remaining budget is accepted",
			cfg:   manifestreader.Config{MaxObjectBytes: math.MaxInt64, MaxTotalBytes: math.MaxInt64},
			sizes: [2]int64{math.MaxInt64 - 10, 10}, wantAccepted: true,
			wantReason: manifestreader.ReasonSizeMismatch, wantCalls: 3,
		},
		{
			name:  "many large objects add up over the total",
			cfg:   manifestreader.Config{MaxTotalBytes: 100, MaxObjectBytes: 60},
			sizes: [2]int64{60, 60}, wantReason: manifestreader.ReasonTotalTooLarge, wantCalls: 1,
		},
		{
			name:  "a negative size is an invalid manifest",
			sizes: [2]int64{-5, 10}, wantReason: string(controlplane.CodeInvalidValue), wantCalls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newFakeStore()
			manifest := manifestOf(t, 1, alpha, beta)
			manifest.Objects[0].Size, manifest.Objects[1].Size = tt.sizes[0], tt.sizes[1]

			store.put(keyOf(t, alpha), []byte(alpha.content))
			store.put(keyOf(t, beta), []byte(beta.content))
			putManifest(t, store, manifest)

			rdr := startRig(t, store, &tt.cfg)

			snap := rdr.snapshot()

			if !tt.wantAccepted {
				assert.Nil(t, snap)
				assert.Equal(t, tt.wantReason, rdr.reader.Status().Manifest.Reason)
				assert.Len(t, store.takeCalls(), tt.wantCalls)

				return
			}

			require.NotNil(t, snap)
			assert.Equal(t, tt.wantReason, snap.Objects[nameAlpha].Reason)
			assert.Len(t, store.takeCalls(), tt.wantCalls)
		})
	}
}

func TestReader_CachedContentIsCheckedAgainstTheSizeOfEachEntry(t *testing.T) {
	t.Parallel()

	alpha, beta, gamma := threeObjects()[0], threeObjects()[1], threeObjects()[2]
	twin := obj{name: nameTwin, content: alpha.content}

	t.Run("a warm cache: the same checksum listed with another size in a later generation", func(t *testing.T) {
		t.Parallel()

		runSteps(t, []step{{
			name: "gen 2 lists alpha with 10 bytes",
			act: func(t *testing.T, store *fakeStore) {
				t.Helper()

				manifest := manifestOf(t, 2, alpha, beta, gamma)
				manifest.Objects[0].Size = 10
				putManifest(t, store, manifest)
			},
			wantCalls: condGet,
			wantSnap: snapOf(2, map[string]manifestreader.ObjectState{
				nameAlpha: func() manifestreader.ObjectState {
					state := okState(t, alpha)
					state.State, state.Reason = manifestreader.StateStale, manifestreader.ReasonSizeMismatch

					return state
				}(),
				nameBeta: okState(t, beta), nameGamma: okState(t, gamma),
			}),
			check: func(t *testing.T, status manifestreader.Status) {
				t.Helper()

				assert.Equal(t, []manifestreader.ObjectFailure{{
					Name: nameAlpha, State: manifestreader.StateStale, Reason: manifestreader.ReasonSizeMismatch,
					Checksum: controlplane.ChecksumOf([]byte(alpha.content)).Value, Retryable: false,
				}}, status.Failures, "no retry can fix it: only a new manifest")
			},
		}})
	})

	t.Run("a cold start: two entries with one checksum and two sizes", func(t *testing.T) {
		t.Parallel()

		store := newFakeStore()
		manifest := manifestOf(t, 1, alpha, twin)
		manifest.Objects[1].Size = 5

		store.put(keyOf(t, alpha), []byte(alpha.content))
		putManifest(t, store, manifest)

		rdr := startRig(t, store, &manifestreader.Config{})

		snap := rdr.snapshot()
		require.NotNil(t, snap)
		assert.Equal(
			t,
			manifestreader.StateOK,
			snap.Objects[nameAlpha].State,
			"the entry with the right size is served",
		)
		assert.Equal(t, manifestreader.StateMissing, snap.Objects[nameTwin].State)
		assert.Equal(t, manifestreader.ReasonSizeMismatch, snap.Objects[nameTwin].Reason)
	})
}

func TestReader_ChannelIsPartOfTheIdentityAtAnEqualGeneration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		channel     string
		wantOutcome string
		wantChannel string
		wantCalls   int
	}{
		{
			name: "another channel with the same objects is a conflict", channel: otherChannel,
			wantOutcome: manifestreader.OutcomeConflict, wantCalls: 1, wantChannel: testChannel,
		},
		{
			name: "the same channel is not", channel: testChannel,
			wantOutcome: manifestreader.OutcomeAccepted, wantCalls: 1, wantChannel: testChannel,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newFakeStore()
			publish(t, store, 1, threeObjects()...)

			rdr := startRig(t, store, &manifestreader.Config{Channel: anyChannel})
			require.NotNil(t, rdr.snapshot())

			held := manifestETag(store)
			other := manifestOf(t, 1, threeObjects()...)
			other.Channel = tt.channel
			createdAt := clocktestInstant()
			other.CreatedAt = &createdAt // other bytes, so that the ETag changes even for the same channel
			putManifest(t, store, other)

			assert.Equal(t, []call{manifestCall(held)}, rdr.round())
			assert.Nil(t, rdr.snapshot(), "no new snapshot either way")
			assert.Equal(t, tt.wantOutcome, rdr.reader.Status().Manifest.Outcome)
			assert.Equal(t, tt.wantChannel, rdr.reader.Status().Manifest.Channel)
		})
	}
}

func TestReader_ConnectionChangeAtAnEqualGeneration(t *testing.T) {
	t.Parallel()

	beta := threeObjects()[1]

	tests := []struct {
		name       string
		wantState  manifestreader.State
		wantReason string
		known      bool
	}{
		{name: "a connection the reader knows: nothing changes", known: true},
		{
			name:      "a connection the reader does not know: that entry fails with its own reason",
			wantState: manifestreader.StateStale, wantReason: manifestreader.ReasonUnknownConnection,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newFakeStore()
			publish(t, store, 1, threeObjects()...)

			opts := []manifestreader.Option{}

			if tt.known {
				secondary := newFakeStore()
				store.shareOrder(secondary)

				opts = append(opts, manifestreader.WithObjectStores(map[string]manifestreader.ObjectStore{
					connElsewhere: secondary,
				}))
			}

			rdr := startRig(t, store, &manifestreader.Config{}, opts...)
			require.NotNil(t, rdr.snapshot())

			held := manifestETag(store)
			manifest := manifestOf(t, 1, threeObjects()...)
			manifest.Objects[1].Connection = connElsewhere
			putManifest(t, store, manifest)

			assert.Equal(t, []call{manifestCall(held)}, rdr.round())

			snap := rdr.snapshot()
			if tt.wantState == "" {
				assert.Nil(t, snap)
				assert.Equal(t, manifestreader.OutcomeAccepted, rdr.reader.Status().Manifest.Outcome)

				return
			}

			require.NotNil(t, snap)
			assert.Equal(t, tt.wantState, snap.Objects[nameBeta].State)
			assert.Equal(t, tt.wantReason, snap.Objects[nameBeta].Reason)
			assert.Equal(t, beta.content, string(snap.Objects[nameBeta].Bytes), "the last good content is carried")
		})
	}
}

func TestReader_AnEntryThatFailsItsOwnChecksNeverBorrowsASibling(t *testing.T) {
	t.Parallel()

	alpha, beta, gamma := threeObjects()[0], threeObjects()[1], threeObjects()[2]
	twin := obj{name: nameTwin, content: alpha.content}
	altKey := keyPrefix + "alt/twin.json"

	// the twin lists the content of alpha under a key that starts with a slash
	build := func(t *testing.T, bad func(entry *controlplane.ManifestObject)) (*fakeStore, *controlplane.Manifest) {
		t.Helper()

		manifest := manifestOf(t, 1, alpha, twin)
		manifest.Objects[1].Key = altKey
		bad(&manifest.Objects[1])

		store := newFakeStore()
		store.put(keyOf(t, alpha), []byte(alpha.content))
		store.put(altKey, []byte(alpha.content))
		putManifest(t, store, manifest)

		return store, manifest
	}

	tests := []struct {
		name       string
		bad        func(entry *controlplane.ManifestObject)
		wantReason string
	}{
		{
			name:       "a key with a leading slash",
			bad:        func(e *controlplane.ManifestObject) { e.Key = "/twin.json" },
			wantReason: manifestreader.ReasonInvalidKey,
		},
		{
			name:       "an unknown connection",
			bad:        func(e *controlplane.ManifestObject) { e.Connection = connElsewhere },
			wantReason: manifestreader.ReasonUnknownConnection,
		},
		{
			name:       "a size above the object limit",
			bad:        func(e *controlplane.ManifestObject) { e.Size = 1 << 40 },
			wantReason: manifestreader.ReasonTooLarge,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name+", cold cache", func(t *testing.T) {
			t.Parallel()

			store, _ := build(t, tt.bad)
			rdr := startRig(t, store, &manifestreader.Config{MaxObjectBytes: 1 << 20, MaxTotalBytes: 1 << 30})

			snap := rdr.snapshot()
			require.NotNil(t, snap)
			assert.Equal(t, manifestreader.StateOK, snap.Objects[nameAlpha].State, "the valid sibling is served")
			assert.Equal(t, manifestreader.StateMissing, snap.Objects[nameTwin].State)
			assert.Equal(t, tt.wantReason, snap.Objects[nameTwin].Reason)
			assert.Equal(
				t,
				[]call{manifestCall(""), objCall(t, alpha)},
				store.takeCalls(),
				"the bad entry costs no request",
			)
		})

		t.Run(tt.name+", warm cache", func(t *testing.T) {
			t.Parallel()

			// generation 1 holds the content; generation 2 lists a twin with the bad entry
			store := newFakeStore()
			publish(t, store, 1, alpha, beta, gamma)

			rdr := startRig(t, store, &manifestreader.Config{MaxObjectBytes: 1 << 20, MaxTotalBytes: 1 << 30})
			require.NotNil(t, rdr.snapshot())

			held := manifestETag(store)
			manifest := manifestOf(t, 2, alpha, beta, gamma, twin)
			tt.bad(&manifest.Objects[3])
			putManifest(t, store, manifest)

			assert.Equal(t, []call{manifestCall(held)}, rdr.round(), "the content is cached: no object request")

			snap := rdr.snapshot()
			require.NotNil(t, snap)
			assert.Equal(t, manifestreader.StateOK, snap.Objects[nameAlpha].State)
			assert.Equal(
				t,
				manifestreader.StateMissing,
				snap.Objects[nameTwin].State,
				"no earlier content for the twin",
			)
			assert.Equal(t, tt.wantReason, snap.Objects[nameTwin].Reason)
		})
	}
}

func TestReader_GroupFailureIsRetryableIfAnyRouteIs(t *testing.T) {
	t.Parallel()

	alpha := threeObjects()[0]
	twin := obj{name: nameTwin, content: alpha.content}
	altKey := keyPrefix + "alt/twin.json"

	denied := &manifestreader.FetchError{Err: errBoom, Reason: manifestreader.ReasonDenied}
	transient := &manifestreader.FetchError{Err: errBoom, Reason: manifestreader.ReasonError, Retryable: true}

	tests := []struct {
		name string
		// first and second are the failures of the first and of the second route.
		first, second error
		wantReason    string
		wantRetryable bool
	}{
		{
			name:          "denied then transient",
			first:         denied,
			second:        transient,
			wantReason:    manifestreader.ReasonDenied,
			wantRetryable: true,
		},
		{
			name:          "transient then denied",
			first:         transient,
			second:        denied,
			wantReason:    manifestreader.ReasonDenied,
			wantRetryable: true,
		},
		{
			name:       "denied on both routes is not retryable",
			first:      denied,
			second:     denied,
			wantReason: manifestreader.ReasonDenied,
		},
		{
			name:          "missing then transient: missing is the more actionable",
			first:         manifestreader.ErrNotFound,
			second:        transient,
			wantReason:    manifestreader.ReasonMissing,
			wantRetryable: true,
		},
		{
			name:          "transient then missing",
			first:         transient,
			second:        manifestreader.ErrNotFound,
			wantReason:    manifestreader.ReasonMissing,
			wantRetryable: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			manifest := manifestOf(t, 1, alpha, twin)
			manifest.Objects[1].Key = altKey

			store := newFakeStore()
			putManifest(t, store, manifest)
			store.failNext(keyOf(t, alpha), tt.first)
			store.failNext(altKey, tt.second)

			rdr := startRig(t, store, &manifestreader.Config{})

			require.NotNil(t, rdr.snapshot())
			assert.Equal(t, []manifestreader.ObjectFailure{
				{
					Name:      nameAlpha,
					State:     manifestreader.StateMissing,
					Reason:    tt.wantReason,
					Checksum:  controlplane.ChecksumOf([]byte(alpha.content)).Value,
					Retryable: tt.wantRetryable,
				},
				{
					Name:      nameTwin,
					State:     manifestreader.StateMissing,
					Reason:    tt.wantReason,
					Checksum:  controlplane.ChecksumOf([]byte(alpha.content)).Value,
					Retryable: tt.wantRetryable,
				},
			}, rdr.reader.Status().Failures)
		})
	}
}

func TestReader_RejectedRestoreDropsTheSavedObjects(t *testing.T) {
	t.Parallel()

	saved := func(manifest []byte) manifestreader.Hooks {
		return manifestreader.Hooks{Restore: func() (*manifestreader.Restored, error) {
			return &manifestreader.Restored{
				ETag:     `"x"`,
				Manifest: manifest,
				Objects:  objectsContent(threeObjects()),
			}, nil
		}}
	}

	good := mustMarshal(t, manifestOf(t, 1, threeObjects()...))

	tests := []struct {
		name        string
		wantOutcome string
		manifest    []byte
		wantCache   manifestreader.CacheStatus
	}{
		{
			name: "an accepted restore keeps the saved objects", manifest: good,
			wantCache: manifestreader.CacheStatus{Objects: 3, Bytes: 32}, wantOutcome: manifestreader.OutcomeAccepted,
		},
		{
			name: "a rejected restore drops them at once", manifest: []byte("garbage: ["),
			wantCache: manifestreader.CacheStatus{}, wantOutcome: manifestreader.OutcomeInvalid,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// the live manifest cannot be read
			own := newFakeStore()
			publish(t, own, 1, threeObjects()...)
			own.failNext(manifestKey, errBoom)

			rdr := startRig(t, own, &manifestreader.Config{}, manifestreader.WithHooks(saved(tt.manifest)))

			status := rdr.reader.Status()
			assert.Equal(t, manifestreader.PollError, status.Poll.Result, "the live poll failed")
			assert.Equal(t, tt.wantOutcome, status.Manifest.Outcome)
			assert.Equal(t, tt.wantCache, status.Cache)
		})
	}
}

func TestReader_AnEntryThatFailedItsOwnChecksPinsNoCachedBytes(t *testing.T) {
	t.Parallel()

	alpha, beta, gamma := threeObjects()[0], threeObjects()[1], threeObjects()[2]
	big := obj{name: "big", content: alpha.content} // the content of alpha, under a name whose size is too large

	store := newFakeStore()
	publish(t, store, 1, alpha, beta, gamma)

	rdr := startRig(t, store, &manifestreader.Config{MaxObjectBytes: 20})
	require.NotNil(t, rdr.snapshot())

	assert.Equal(t, manifestreader.CacheStatus{Objects: 3, Bytes: 32}, rdr.reader.Status().Cache, "generation 1")

	// generation 2: alpha is gone, and its content is only listed by an entry that is too large to be requested
	held := manifestETag(store)
	manifest := manifestOf(t, 2, big, beta, gamma)
	manifest.Objects[0].Size = 30
	putManifest(t, store, manifest)

	assert.Equal(t, []call{manifestCall(held)}, rdr.round())

	snap := rdr.snapshot()
	require.NotNil(t, snap)
	assert.Equal(t, manifestreader.ReasonTooLarge, snap.Objects["big"].Reason)
	assert.Equal(t, manifestreader.CacheStatus{Objects: 2, Bytes: 21}, rdr.reader.Status().Cache,
		"the bytes of alpha are not pinned by an entry that never serves them")
}

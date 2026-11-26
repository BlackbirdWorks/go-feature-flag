package manifestreader_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/internal/manifestreader"
)

var errBoom = errors.New("boom: SECRET-DETAIL")

// staleAlpha is alpha as a snapshot carries it while the new content cannot be loaded: the bytes and the
// checksum of generation 1.
func staleAlpha(tb testing.TB, reason string) manifestreader.ObjectState {
	tb.Helper()

	state := okState(tb, threeObjects()[0])
	state.State = manifestreader.StateStale
	state.Reason = reason

	return state
}

func TestReader_ObjectFailureAndRecovery(t *testing.T) {
	t.Parallel()

	beta, gamma := threeObjects()[1], threeObjects()[2]
	alpha2 := obj{name: nameAlpha, content: contentAlphaSecond}
	alpha2Key := keyOf(t, alpha2)

	// publishWithout writes generation 2 and the content of every object but alpha2
	publishWithout := func(t *testing.T, store *fakeStore) {
		t.Helper()

		store.put(keyOf(t, beta), []byte(beta.content))
		store.put(keyOf(t, gamma), []byte(gamma.content))
		putManifest(t, store, manifestOf(t, 2, alpha2, beta, gamma))
	}

	tests := []struct {
		name string
		// fail makes alpha2 unloadable after generation 2 was published; heal makes it loadable.
		fail          func(t *testing.T, store *fakeStore)
		heal          func(t *testing.T, store *fakeStore)
		wantReason    string
		wantRetryable bool
	}{
		{
			name:       "404: the object listed by the manifest is not there",
			fail:       func(*testing.T, *fakeStore) {},
			heal:       func(_ *testing.T, store *fakeStore) { store.put(alpha2Key, []byte(alpha2.content)) },
			wantReason: manifestreader.ReasonMissing, wantRetryable: true,
		},
		{
			name:       "corrupt: same length, other bytes",
			fail:       func(_ *testing.T, store *fakeStore) { store.put(alpha2Key, []byte(`{"alpha":"SECOND"}`)) },
			heal:       func(_ *testing.T, store *fakeStore) { store.put(alpha2Key, []byte(alpha2.content)) },
			wantReason: manifestreader.ReasonChecksumMismatch, wantRetryable: true,
		},
		{
			name:          "over size: more bytes than the manifest lists",
			fail:          func(_ *testing.T, store *fakeStore) { store.put(alpha2Key, []byte(alpha2.content+" and more")) },
			heal:          func(_ *testing.T, store *fakeStore) { store.put(alpha2Key, []byte(alpha2.content)) },
			wantReason:    manifestreader.ReasonSizeMismatch,
			wantRetryable: true,
		},
		{
			name: "transient error twice, then it passes",
			fail: func(_ *testing.T, store *fakeStore) {
				store.put(alpha2Key, []byte(alpha2.content))
				store.failNext(alpha2Key,
					&manifestreader.FetchError{Err: errBoom, Reason: manifestreader.ReasonError, Retryable: true},
					&manifestreader.FetchError{Err: errBoom, Reason: manifestreader.ReasonError, Retryable: true})
			},
			heal:       func(*testing.T, *fakeStore) {},
			wantReason: manifestreader.ReasonError, wantRetryable: true,
		},
		{
			name: "denied is reported as not retryable and is still tried again",
			fail: func(_ *testing.T, store *fakeStore) {
				store.put(alpha2Key, []byte(alpha2.content))
				store.failNext(alpha2Key,
					&manifestreader.FetchError{Err: errBoom, Reason: manifestreader.ReasonDenied},
					&manifestreader.FetchError{Err: errBoom, Reason: manifestreader.ReasonDenied})
			},
			heal:       func(*testing.T, *fakeStore) {},
			wantReason: manifestreader.ReasonDenied,
		},
		{
			name: "an error that is not a FetchError counts as a transient one",
			fail: func(_ *testing.T, store *fakeStore) {
				store.put(alpha2Key, []byte(alpha2.content))
				store.failNext(alpha2Key, errBoom, errBoom)
			},
			heal:       func(*testing.T, *fakeStore) {},
			wantReason: manifestreader.ReasonError, wantRetryable: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Helper()

			t.Parallel()

			store := newFakeStore()
			publish(t, store, 1, threeObjects()...)

			rdr := startRig(t, store, &manifestreader.Config{})
			require.NotNil(t, rdr.snapshot())

			etag1 := manifestETag(store)

			publishWithout(t, store)
			tt.fail(t, store)

			wantFailure := []manifestreader.ObjectFailure{{
				Name: nameAlpha, State: manifestreader.StateStale, Reason: tt.wantReason,
				Checksum: controlplane.ChecksumOf([]byte(alpha2.content)).Value, Retryable: tt.wantRetryable,
			}}
			wantStale := snapOf(2, map[string]manifestreader.ObjectState{
				nameAlpha: staleAlpha(t, tt.wantReason), nameBeta: okState(t, beta), nameGamma: okState(t, gamma),
			})

			// the round that reads generation 2: the failing object does not block the others
			assert.Equal(t, []call{manifestCall(etag1), {Key: alpha2Key}}, rdr.round(), "gen 2 round")
			assert.Equal(t, wantStale, rdr.snapshot())
			current := rdr.reader.Status()
			assertFailing(t, &current, wantFailure)

			etag2 := manifestETag(store)

			// still failing: the manifest is a 304 and only the failed object is fetched again
			assert.Equal(t, []call{manifestCall(etag2), {Key: alpha2Key}}, rdr.round(), "retry round")
			assert.Nil(t, rdr.snapshot(), "no state change, no snapshot")
			current = rdr.reader.Status()
			assertFailing(t, &current, wantFailure)

			tt.heal(t, store)

			// the retry succeeds: a new snapshot of the same generation
			assert.Equal(t, []call{manifestCall(etag2), {Key: alpha2Key}}, rdr.round(), "recovery round")
			assert.Equal(t, snapOf(2, map[string]manifestreader.ObjectState{
				nameAlpha: okState(t, alpha2), nameBeta: okState(t, beta), nameGamma: okState(t, gamma),
			}), rdr.snapshot())

			status := rdr.reader.Status()
			assert.Empty(t, status.Failures)
			assert.Equal(t, manifestreader.Counts{OK: 3}, status.Counts)

			// and quiet again
			assert.Equal(t, []call{manifestCall(etag2)}, rdr.round())
			assert.Nil(t, rdr.snapshot())
		})
	}
}

func assertFailing(t *testing.T, status *manifestreader.Status, want []manifestreader.ObjectFailure) {
	t.Helper()

	assert.Equal(t, want, status.Failures)
	assert.Equal(t, manifestreader.Counts{OK: 2, Stale: 1}, status.Counts)
	assert.Equal(t, accepted(2, 3), status.Manifest)
}

func TestReader_ColdStartWithAFailingObjectIsMissingThenLoads(t *testing.T) {
	t.Parallel()

	alpha, beta, gamma := threeObjects()[0], threeObjects()[1], threeObjects()[2]
	store := newFakeStore()

	// alpha is listed but not written
	store.put(keyOf(t, beta), []byte(beta.content))
	store.put(keyOf(t, gamma), []byte(gamma.content))
	putManifest(t, store, manifestOf(t, 1, alpha, beta, gamma))

	rdr := startRig(t, store, &manifestreader.Config{})

	missing := manifestreader.ObjectState{
		Kind: controlplane.KindFlags, Format: controlplane.FormatJSON,
		Checksum: controlplane.ChecksumOf([]byte(alpha.content)), State: manifestreader.StateMissing,
		Reason: manifestreader.ReasonMissing,
	}

	assert.Equal(t, snapOf(1, map[string]manifestreader.ObjectState{
		nameAlpha: missing, nameBeta: okState(t, beta), nameGamma: okState(t, gamma),
	}), rdr.snapshot())
	assert.Equal(t, []manifestreader.ObjectFailure{{
		Name: nameAlpha, State: manifestreader.StateMissing, Reason: manifestreader.ReasonMissing,
		Checksum: controlplane.ChecksumOf([]byte(alpha.content)).Value, Retryable: true,
	}}, rdr.reader.Status().Failures)
	assert.Equal(t, manifestreader.Counts{OK: 2, Missing: 1}, rdr.reader.Status().Counts)

	etag := manifestETag(store)
	store.put(keyOf(t, alpha), []byte(alpha.content))

	assert.Equal(t, []call{manifestCall(etag), objCall(t, alpha)}, rdr.round())
	assert.Equal(t, snapOf(1, map[string]manifestreader.ObjectState{
		nameAlpha: okState(t, alpha), nameBeta: okState(t, beta), nameGamma: okState(t, gamma),
	}), rdr.snapshot())
}

func TestReader_StaleContentSurvivesLaterGenerations(t *testing.T) {
	t.Parallel()

	beta, gamma := threeObjects()[1], threeObjects()[2]
	alpha2 := obj{name: nameAlpha, content: contentAlphaSecond}
	beta2 := obj{name: nameBeta, content: `{"beta":"second"}`}

	store := newFakeStore()
	publish(t, store, 1, threeObjects()...)

	rdr := startRig(t, store, &manifestreader.Config{})
	require.NotNil(t, rdr.snapshot())

	// generation 2: alpha2 is not written
	store.put(keyOf(t, beta), []byte(beta.content))
	store.put(keyOf(t, gamma), []byte(gamma.content))
	putManifest(t, store, manifestOf(t, 2, alpha2, beta, gamma))
	rdr.round()
	require.NotNil(t, rdr.snapshot())

	// generation 3 changes beta only: alpha is still stale with the bytes of generation 1
	publish(t, store, 3, alpha2, beta2, gamma)
	store.remove(keyOf(t, alpha2))

	etag2 := etagOf(mustMarshal(t, manifestOf(t, 2, alpha2, beta, gamma)))
	assert.Equal(t, []call{manifestCall(etag2), {Key: keyOf(t, alpha2)}, objCall(t, beta2)}, rdr.round())

	assert.Equal(t, snapOf(3, map[string]manifestreader.ObjectState{
		nameAlpha: staleAlpha(
			t,
			manifestreader.ReasonMissing,
		),
		nameBeta:  okState(t, beta2),
		nameGamma: okState(t, gamma),
	}), rdr.snapshot())

	// the manifest drops alpha: removal is not a failure
	publish(t, store, 4, beta2, gamma)
	rdr.round()
	assert.Equal(t, snapOf(4, map[string]manifestreader.ObjectState{
		nameBeta: okState(t, beta2), nameGamma: okState(t, gamma),
	}), rdr.snapshot())
	assert.Empty(t, rdr.reader.Status().Failures)
}

func TestReader_ObjectLargerThanTheObjectLimitIsNotRequested(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	publish(t, store, 1, threeObjects()...)

	rdr := startRig(t, store, &manifestreader.Config{MaxObjectBytes: 10})

	// every object is 10 bytes or more: {"beta":2} is 10, {"alpha":1} is 11, {"gamma":3} is 11
	assert.Equal(t, []call{manifestCall(""), objCall(t, threeObjects()[1])}, store.takeCalls())

	snap := rdr.snapshot()
	require.NotNil(t, snap)
	assert.Equal(t, manifestreader.StateOK, snap.Objects[nameBeta].State)
	assert.Equal(t, manifestreader.StateMissing, snap.Objects[nameAlpha].State)
	assert.Equal(t, manifestreader.ReasonTooLarge, snap.Objects[nameAlpha].Reason)
	assert.Nil(t, snap.Objects[nameAlpha].Bytes)
}

func TestReader_UnknownConnection(t *testing.T) {
	t.Parallel()

	alpha, beta := threeObjects()[0], threeObjects()[1]
	main, other := newFakeStore(), newFakeStore()
	main.shareOrder(other)

	manifest := manifestOf(t, 1, alpha, beta)
	manifest.Objects[1].Connection = connElsewhere
	manifest.Objects = append(manifest.Objects, entryOf(t, threeObjects()[2]))
	manifest.Objects[2].Connection = "unknown"

	main.put(keyOf(t, alpha), []byte(alpha.content))
	other.put(keyOf(t, beta), []byte(beta.content))
	putManifest(t, main, manifest)

	rdr := startRig(
		t,
		main,
		&manifestreader.Config{},
		manifestreader.WithObjectStores(map[string]manifestreader.ObjectStore{
			connControl: main, connElsewhere: other,
		}),
	)

	snap := rdr.snapshot()
	require.NotNil(t, snap)
	assert.Equal(t, manifestreader.StateOK, snap.Objects[nameAlpha].State)
	assert.Equal(t, manifestreader.StateOK, snap.Objects[nameBeta].State, "read from the store of its connection")
	assert.Equal(t, manifestreader.StateMissing, snap.Objects[nameGamma].State)
	assert.Equal(t, manifestreader.ReasonUnknownConnection, snap.Objects[nameGamma].Reason)
	assert.Equal(t, []call{objCall(t, beta)}, other.takeCalls())
}

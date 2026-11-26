package manifestreader_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/internal/manifestreader"
	"github.com/blackbirdworks/semaphore/internal/manifestreader/statedir"
)

// TestRestart_AStaleNameIsStaleAgainWithItsOldBytes: generation 2 changes alpha and its new object is missing, so
// alpha is served stale from generation 1. After a restart with the store down alpha is stale with the
// generation 1 bytes, not missing; when the store has the object again it is ok.
func TestRestart_AStaleNameIsStaleAgainWithItsOldBytes(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	objs := threeObjects()
	alpha2 := obj{name: nameAlpha, content: contentAlphaSecond}

	publish(t, store, 1, objs...)

	dir := filepath.Join(t.TempDir(), "state")

	first, state := startWithState(t, store, dir, nil)
	require.NotNil(t, first.snapshot())

	// generation 2: the manifest lists the new alpha, whose object is not there
	publish(t, store, 2, alpha2, objs[1], objs[2])
	store.remove(keyOf(t, alpha2))
	first.round()

	snap := first.snapshot()
	require.NotNil(t, snap)
	assert.Equal(t, staleAlpha(t, manifestreader.ReasonMissing), snap.Objects[nameAlpha])

	state.flush()
	first.stop()
	state.close()
	store.takeCalls()

	// the restart, with the store down: the manifest cannot be read
	store.failNext(manifestKey, errTransient)

	second, state2 := startWithState(t, store, dir, nil)

	restored := second.snapshot()
	require.NotNil(t, restored)
	assert.Equal(t, uint64(2), restored.Generation)
	assert.Equal(t, staleAlpha(t, manifestreader.ReasonNotRestored), restored.Objects[nameAlpha],
		"stale with the generation 1 bytes and checksum, not missing")
	assert.Equal(t, okState(t, objs[1]), restored.Objects[nameBeta])
	assert.Equal(t, okState(t, objs[2]), restored.Objects[nameGamma])

	status := second.reader.Status()
	assert.Equal(t, manifestreader.Counts{OK: 2, Stale: 1}, status.Counts)
	assert.Equal(t, []manifestreader.ObjectFailure{{
		Name: nameAlpha, State: manifestreader.StateStale, Reason: manifestreader.ReasonNotRestored,
		Checksum: controlplane.ChecksumOf([]byte(alpha2.content)).Value, Retryable: true,
	}}, status.Failures)
	assert.Equal(
		t,
		[]call{manifestCall(manifestETag(store))},
		store.takeCalls(),
		"the failed manifest poll is all it cost",
	)

	// the store has the object again: the next round fetches it and alpha is ok
	store.put(keyOf(t, alpha2), []byte(alpha2.content))

	assert.Equal(t, []call{manifestCall(manifestETag(store)), objCall(t, alpha2)}, second.round())

	healed := second.snapshot()
	require.NotNil(t, healed)
	assert.Equal(t, okState(t, alpha2), healed.Objects[nameAlpha])
	assert.Empty(t, second.reader.Status().Failures)

	state2.flush()
}

// TestRestart_PublishesTheRestoredSnapshotBeforeAnyRequestCompletes: the snapshot is there while the first manifest
// request is still blocked, and nothing but that request was made.
func TestRestart_PublishesTheRestoredSnapshotBeforeAnyRequestCompletes(t *testing.T) {
	t.Parallel()

	objs := threeObjects()

	tests := []struct {
		damage     func(t *testing.T, dir string)
		name       string
		wantAlpha  manifestreader.ObjectState
		wantFailed int
	}{
		{name: "everything restored", damage: func(*testing.T, string) {}, wantAlpha: okState(t, objs[0])},
		{
			name: "one object file corrupt: missing and pending until the first round fetches it",
			damage: func(t *testing.T, dir string) {
				t.Helper()
				require.NoError(t, os.Truncate(fileOf(dir, objs[0].content), 3))
			},
			wantAlpha: manifestreader.ObjectState{
				Kind: controlplane.KindFlags, Format: controlplane.FormatJSON,
				Checksum: controlplane.ChecksumOf([]byte(objs[0].content)), State: manifestreader.StateMissing,
				Reason: manifestreader.ReasonNotRestored,
			},
			wantFailed: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newFakeStore()
			publish(t, store, 1, objs...)

			dir := filepath.Join(t.TempDir(), "state")

			first, state := startWithState(t, store, dir, nil)
			state.flush()
			first.stop()
			state.close()

			tt.damage(t, dir)
			store.takeCalls()

			// the store does not answer: every request blocks until the reader stops
			store.setHang(manifestKey, true)

			state2 := openState(t, dir, nil)
			second := launchRig(t, store, &manifestreader.Config{DisableJitter: true},
				manifestreader.WithHooks(state2.store.Hooks()))

			select {
			case key := <-store.hung:
				assert.Equal(t, manifestKey, key)
			case <-time.After(wait):
				require.FailNow(t, "the first manifest request was not sent")
			}

			snap := second.snapshot()
			require.NotNil(t, snap, "published while the first request is still pending")
			assert.Equal(t, uint64(1), snap.Generation)
			assert.Equal(t, tt.wantAlpha, snap.Objects[nameAlpha])
			assert.Equal(t, okState(t, objs[1]), snap.Objects[nameBeta])
			assert.Equal(t, okState(t, objs[2]), snap.Objects[nameGamma])
			assert.Len(t, second.reader.Status().Failures, tt.wantFailed)
			assert.Equal(
				t,
				[]call{manifestCall(manifestETag(store))},
				store.takeCalls(),
				"no object request before the manifest answers",
			)
		})
	}
}

// TestRestart_ARestoredObjectIsCheckedLikeAFetchedOne: bytes whose checksum is right but whose length is not the
// listed size, or that are above the object limit, are failed locally and never served; nothing is requested for
// them (the manifest poll is the only request).
func TestRestart_ARestoredObjectIsCheckedLikeAFetchedOne(t *testing.T) {
	t.Parallel()

	alpha := threeObjects()[0]

	tests := []struct {
		name           string
		wantAlphaState manifestreader.State
		wantFailures   []manifestreader.ObjectFailure
		cfg            manifestreader.Config
		sizeDelta      int64
	}{
		{name: "the listed size is the length", wantAlphaState: manifestreader.StateOK},
		{
			name: "the listed size is not the length", sizeDelta: 1, wantAlphaState: manifestreader.StateMissing,
			wantFailures: []manifestreader.ObjectFailure{{
				Name: nameAlpha, State: manifestreader.StateMissing, Reason: manifestreader.ReasonSizeMismatch,
				Checksum: controlplane.ChecksumOf([]byte(alpha.content)).Value,
			}},
		},
		{
			name: "the object is above the object limit", cfg: manifestreader.Config{MaxObjectBytes: 4},
			wantAlphaState: manifestreader.StateMissing,
			wantFailures: []manifestreader.ObjectFailure{{
				Name: nameAlpha, State: manifestreader.StateMissing, Reason: manifestreader.ReasonTooLarge,
				Checksum: controlplane.ChecksumOf([]byte(alpha.content)).Value,
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newFakeStore()
			manifest := manifestOf(t, 1, alpha)
			manifest.Objects[0].Size += tt.sizeDelta

			raw := mustMarshal(t, manifest)
			store.put(manifestKey, raw)
			store.put(keyOf(t, alpha), []byte(alpha.content))

			hooks := manifestreader.Hooks{Restore: func() (*manifestreader.Restored, error) {
				return &manifestreader.Restored{
					ETag: etagOf(raw), Manifest: raw, Objects: [][]byte{[]byte(alpha.content)},
				}, nil
			}}

			cfg := tt.cfg
			rdr := startRig(t, store, &cfg, manifestreader.WithHooks(hooks))

			assert.Equal(t, []call{manifestCall(etagOf(raw))}, store.takeCalls())

			snap := rdr.snapshot()
			require.NotNil(t, snap)
			assert.Equal(t, tt.wantAlphaState, snap.Objects[nameAlpha].State)

			if tt.wantAlphaState != manifestreader.StateOK {
				assert.Nil(t, snap.Objects[nameAlpha].Bytes, "the restored bytes are not served")
			}

			assert.Equal(t, tt.wantFailures, rdr.reader.Status().Failures)
		})
	}
}

// TestRestart_ARefusedStateDirectoryIsAColdStartAndIsLeftAlone: a directory that is not ours is not used, not
// changed, and the relay starts cold, with the reason in the status.
func TestRestart_ARefusedStateDirectoryIsAColdStartAndIsLeftAlone(t *testing.T) {
	t.Parallel()

	objs := threeObjects()

	tests := []struct {
		prepare    func(t *testing.T, dir string)
		name       string
		wantReason string
	}{
		{
			name: "another layout version", wantReason: statedir.DisabledForeignVersion,
			prepare: func(t *testing.T, dir string) {
				t.Helper()
				require.NoError(
					t,
					os.WriteFile(filepath.Join(dir, "VERSION"), []byte("semaphore-manifest-state 2\n"), 0o600),
				)
				require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest.json"), []byte("future"), 0o600))
			},
		},
		{
			name: "files of somebody else", wantReason: statedir.DisabledUnsafe,
			prepare: func(t *testing.T, dir string) {
				t.Helper()
				require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("keep me"), 0o600))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newFakeStore()
			publish(t, store, 1, objs...)

			dir := filepath.Join(t.TempDir(), "state")
			require.NoError(t, os.Mkdir(dir, 0o700))
			tt.prepare(t, dir)

			before := dirImage(t, dir)

			disabled, hooks, reason := statedir.OpenOrDisable(statedir.Config{Dir: dir})
			require.Nil(t, disabled)
			assert.Equal(t, tt.wantReason, reason)

			rdr := launchRig(t, store, &manifestreader.Config{DisableJitter: true}, manifestreader.WithHooks(hooks))
			rdr.settle()

			assert.Equal(t, coldCalls(t, objs...), store.takeCalls())
			assert.Equal(t, manifestreader.StateStatus{
				Restore: manifestreader.RestoreDisabled, DisabledReason: tt.wantReason,
			}, rdr.reader.Status().State)
			assert.Equal(t, before, dirImage(t, dir), "the directory is byte-identical")
		})
	}
}

func dirImage(tb testing.TB, dir string) map[string]string {
	tb.Helper()

	out := map[string]string{}

	require.NoError(tb, filepath.WalkDir(dir, func(name string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}

		data, readErr := os.ReadFile(name)
		out[name] = string(data)

		return readErr
	}))

	return out
}

// TestRestart_AStaleNameWhoseEntryFailsItsOwnChecksSurvivesTwoRestarts: generation 2 lists alpha with a connection
// the reader has no store for, so alpha is stale from its generation 1 content without any request. The first
// restart restores it stale (not missing), and the save after it must not delete the fallback: the second restart
// restores it stale again.
func TestRestart_AStaleNameWhoseEntryFailsItsOwnChecksSurvivesTwoRestarts(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	objs := threeObjects()
	publish(t, store, 1, objs...)

	dir := filepath.Join(t.TempDir(), "state")

	first, state := startWithState(t, store, dir, nil)
	require.NotNil(t, first.snapshot())

	next := manifestOf(t, 2, objs...)
	next.Objects[0].Connection = connElsewhere
	putManifest(t, store, next)

	first.round()
	state.flush()

	snap := first.snapshot()
	require.NotNil(t, snap)
	assert.Equal(t, staleAlpha(t, manifestreader.ReasonUnknownConnection), snap.Objects[nameAlpha])

	first.stop()
	state.close()

	want := staleAlpha(t, manifestreader.ReasonUnknownConnection)

	for restart := 1; restart <= 2; restart++ {
		store.takeCalls()

		rdr, restarted := startWithState(t, store, dir, nil)
		restored := rdr.snapshot()
		require.NotNil(t, restored, "restart %d", restart)
		assert.Equal(t, want, restored.Objects[nameAlpha], "restart %d: stale with the generation 1 bytes", restart)
		assert.Equal(t, manifestreader.Counts{OK: 2, Stale: 1}, rdr.reader.Status().Counts, "restart %d", restart)

		restarted.flush()
		rdr.stop()
		restarted.close()
	}
}

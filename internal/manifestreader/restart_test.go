package manifestreader_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/internal/manifestreader"
	"github.com/blackbirdworks/semaphore/internal/manifestreader/statedir"
)

// stateRun is a state directory with its writer running.
type stateRun struct {
	t      testing.TB
	store  *statedir.Store
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

func openState(tb testing.TB, dir string, fsys statedir.FS) *stateRun {
	tb.Helper()

	store, err := statedir.New(statedir.Config{Dir: dir, FS: fsys})
	require.NoError(tb, err)

	ctx, cancel := context.WithCancel(tb.Context())
	run := &stateRun{t: tb, store: store, cancel: cancel, done: make(chan struct{})}

	go func() {
		store.Run(ctx)
		close(run.done)
	}()

	tb.Cleanup(run.close)

	return run
}

func (s *stateRun) flush() {
	s.t.Helper()

	ctx, cancel := context.WithTimeout(s.t.Context(), wait)
	defer cancel()

	require.NoError(s.t, s.store.Flush(ctx))
}

// close stops the writer and releases the directory, so that the next start can take it. It may be called twice.
func (s *stateRun) close() {
	s.once.Do(func() {
		s.cancel()

		select {
		case <-s.done:
			require.NoError(s.t, s.store.Close())
		case <-time.After(wait):
			assert.Fail(s.t, "the writer did not stop")
		}
	})
}

// startWithState starts a reader with the state directory behind its hooks, with the jitter off.
func startWithState(tb testing.TB, store *fakeStore, dir string, fsys statedir.FS) (*rig, *stateRun) {
	tb.Helper()

	state := openState(tb, dir, fsys)
	rdr := launchRig(
		tb,
		store,
		&manifestreader.Config{DisableJitter: true},
		manifestreader.WithHooks(state.store.Hooks()),
	)
	rdr.settle()

	return rdr, state
}

// fileOf is the object file of content in a state directory.
func fileOf(dir, content string) string {
	return filepath.Join(dir, "objects", controlplane.ChecksumOf([]byte(content)).Value)
}

func TestRestart_CostsOneConditionalGetAndNoObjectGet(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	objs := threeObjects()
	publish(t, store, 1, objs...)

	dir := filepath.Join(t.TempDir(), "state")

	first, state := startWithState(t, store, dir, nil)
	first.round()
	state.flush()
	first.stop()
	state.close()

	store.takeCalls()

	// the restart: the snapshot is there before any request is answered, and the whole cost is one 304
	second, state2 := startWithState(t, store, dir, nil)

	snap := second.snapshot()
	require.NotNil(t, snap)
	assert.Equal(t, uint64(1), snap.Generation)

	for _, o := range objs {
		assert.Equal(t, okState(t, o), snap.Objects[o.name])
	}

	assert.Equal(
		t,
		[]call{manifestCall(manifestETag(store))},
		store.takeCalls(),
		"one conditional GET, the ETag of the accepted manifest",
	)

	status := second.reader.Status()
	assert.Equal(t, manifestreader.PollNotModified, status.Poll.Result)
	assert.Equal(t, accepted(1, 3), status.Manifest)

	// the save the reader makes after the restore finds everything on disk and writes nothing; the next round's
	// status shows it
	state2.flush()
	assert.Equal(t, []call{manifestCall(manifestETag(store))}, second.round())
	assert.Equal(t, manifestreader.StateStatus{
		Enabled: true, Restore: manifestreader.RestoreRestored, RestoredObjects: 3,
	}, second.reader.Status().State, "nothing was saved again")
}

func TestRestart_WithTheStoreDownServesTheSavedContent(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	objs := threeObjects()
	publish(t, store, 1, objs...)

	dir := filepath.Join(t.TempDir(), "state")

	first, state := startWithState(t, store, dir, nil)
	state.flush()
	first.stop()
	state.close()

	store.takeCalls()

	// the store is unreachable: the manifest and every object fail
	etag := manifestETag(store)
	store.failNext(manifestKey, errTransient)

	second, _ := startWithState(t, store, dir, nil)

	snap := second.snapshot()
	require.NotNil(t, snap, "served at once, without a successful request")
	assert.Equal(t, uint64(1), snap.Generation)

	for _, o := range objs {
		assert.Equal(t, okState(t, o), snap.Objects[o.name])
	}

	assert.Equal(t, []call{manifestCall(etag)}, store.takeCalls(), "the failed poll is the only request")
	assert.Equal(t, manifestreader.PollError, second.reader.Status().Poll.Result)
	assert.Equal(t, manifestreader.Counts{OK: 3}, second.reader.Status().Counts)
	assert.Equal(t, 1, second.reader.Status().Backoff.Failures)
}

func TestRestart_CorruptedFilesCostExactlyWhatTheyShould(t *testing.T) {
	t.Parallel()

	objs := threeObjects()
	alpha := objs[0]

	// damage is what happens to the state directory between the two runs
	tests := []struct {
		damage    func(t *testing.T, dir string)
		fsys      func() statedir.FS
		wantCalls func(t *testing.T, etag string) []call
		name      string
	}{
		{
			name: "nothing", damage: func(*testing.T, string) {},
			wantCalls: objectCalls(),
		},
		{
			name: "an object truncated: one object GET",
			damage: func(t *testing.T, dir string) {
				t.Helper()
				require.NoError(t, os.Truncate(fileOf(dir, alpha.content), 3))
			},
			wantCalls: objectCalls(alpha),
		},
		{
			name: "an object with a flipped byte: one object GET",
			damage: func(t *testing.T, dir string) {
				t.Helper()

				data, err := os.ReadFile(fileOf(dir, alpha.content))
				require.NoError(t, err)

				data[0] ^= 0xff
				require.NoError(t, os.WriteFile(fileOf(dir, alpha.content), data, 0o600))
			},
			wantCalls: objectCalls(alpha),
		},
		{
			name: "an object file missing: one object GET",
			damage: func(t *testing.T, dir string) {
				t.Helper()
				require.NoError(t, os.Remove(fileOf(dir, alpha.content)))
			},
			wantCalls: objectCalls(alpha),
		},
		{
			name: "all objects missing: the three object GETs",
			damage: func(t *testing.T, dir string) {
				t.Helper()

				for _, o := range objs {
					require.NoError(t, os.Remove(fileOf(dir, o.content)))
				}
			},
			wantCalls: objectCalls(objs...),
		},
		{
			name: "manifest copy corrupt: a cold start",
			damage: func(t *testing.T, dir string) {
				t.Helper()
				require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest.json"), []byte("{broken"), 0o600))
			},
			wantCalls: coldCalls(objs),
		},
		{
			name: "manifest copy truncated: a cold start",
			damage: func(t *testing.T, dir string) {
				t.Helper()
				require.NoError(t, os.Truncate(filepath.Join(dir, "manifest.json"), 20))
			},
			wantCalls: coldCalls(objs),
		},
		{
			name: "manifest copy missing: a cold start",
			damage: func(t *testing.T, dir string) {
				t.Helper()
				require.NoError(t, os.Remove(filepath.Join(dir, "manifest.json")))
			},
			wantCalls: coldCalls(objs),
		},
		{
			name: "an empty directory: a cold start",
			damage: func(t *testing.T, dir string) {
				t.Helper()
				require.NoError(t, os.RemoveAll(dir))
				require.NoError(t, os.Mkdir(dir, 0o700))
			},
			wantCalls: coldCalls(objs),
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

			var fsys statedir.FS

			etag := manifestETag(store)
			second, state2 := startWithState(t, store, dir, fsys)

			assert.Equal(t, sortCalls(tt.wantCalls(t, etag)), store.takeCalls())

			snap := second.snapshot()
			require.NotNil(t, snap)

			for _, o := range objs {
				assert.Equal(t, okState(t, o), snap.Objects[o.name], "every object is served verified")
			}

			// whatever was damaged is repaired by the run: the next restart costs one conditional GET
			state2.flush()
			second.stop()
			state2.close()

			third, _ := startWithState(t, store, dir, nil)
			assert.Equal(t, []call{manifestCall(etag)}, store.takeCalls(), "repaired")
			assert.Equal(t, manifestreader.Counts{OK: 3}, third.reader.Status().Counts)
		})
	}
}

// objectCalls is what a restart costs: the conditional manifest GET and one GET for each of the objects.
func objectCalls(items ...obj) func(*testing.T, string) []call {
	return func(t *testing.T, etag string) []call {
		t.Helper()

		out := make([]call, 0, 1+len(items))
		out = append(out, manifestCall(etag))

		for _, item := range items {
			out = append(out, objCall(t, item))
		}

		return out
	}
}

func coldCalls(objs []obj) func(*testing.T, string) []call {
	return func(t *testing.T, _ string) []call {
		t.Helper()

		out := make([]call, 0, 1+len(objs))
		out = append(out, manifestCall(""))

		for _, o := range objs {
			out = append(out, objCall(t, o))
		}

		return out
	}
}

// failingRoot fails the reads and, when createErr is set, the creation of files.
type failingRoot struct {
	statedir.Root

	readErr   error
	createErr error
}

func (f failingRoot) ReadFile(name string, limit int64) ([]byte, error) {
	if f.readErr != nil {
		return nil, f.readErr
	}

	return f.Root.ReadFile(name, limit)
}

func (f failingRoot) Create(name string) (statedir.File, error) { //nolint:ireturn // implements the FS seam
	if f.createErr != nil {
		return nil, f.createErr
	}

	return f.Root.Create(name)
}

// fullDiskFS opens the directory and fails every file creation with ENOSPC.
type fullDiskFS struct{ statedir.FS }

func (u fullDiskFS) Open(dir string) (statedir.Root, error) { //nolint:ireturn // implements the FS seam
	root, err := u.FS.Open(dir)
	if err != nil {
		return nil, err
	}

	return failingRoot{Root: root, createErr: syscall.ENOSPC}, nil
}

func TestRestart_ASaveFailureNeverStopsPolling(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	objs := threeObjects()
	publish(t, store, 1, objs...)

	dir := filepath.Join(t.TempDir(), "state")

	rdr, state := startWithState(t, store, dir, fullDiskFS{statedir.NewOSFS()})
	require.NotNil(t, rdr.snapshot())
	state.flush()

	alpha2 := obj{name: nameAlpha, content: contentAlphaChanged}
	publish(t, store, 2, alpha2, objs[1], objs[2])

	// polling goes on and delivers: the disk is full, and that is all
	rdr.round()

	snap := rdr.snapshot()
	require.NotNil(t, snap)
	assert.Equal(t, uint64(2), snap.Generation)

	state.flush()
	rdr.round()

	status := rdr.reader.Status()
	assert.Equal(t, manifestreader.PollNotModified, status.Poll.Result)
	assert.Equal(t, uint64(2), status.State.SaveErrors)
	assert.Equal(t, "no-space", status.State.LastSaveError)
	assert.Equal(t, manifestreader.Counts{OK: 3}, status.Counts)
}

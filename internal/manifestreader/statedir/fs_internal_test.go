package statedir

import (
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/manifestreader"
)

var errSpy = errors.New("sync failed")

// TestOSFS_OpenFlushesTheParentOfEachDirectoryItCreates: a state directory that Open creates is reachable after a
// power loss only when the entry of every created directory is durable, so the parent of each is flushed, top down;
// an existing directory costs no flush.
func TestOSFS_OpenFlushesTheParentOfEachDirectoryItCreates(t *testing.T) {
	t.Parallel()

	base := t.TempDir()

	tests := []struct {
		syncErr    error
		wantErr    error
		name       string
		dir        string
		wantSynced []string
	}{
		{name: "three new directories", dir: filepath.Join(base, "a", "b", "c"), wantSynced: []string{
			base, filepath.Join(base, "a"), filepath.Join(base, "a", "b"),
		}},
		{name: "one new directory", dir: filepath.Join(base, "one"), wantSynced: []string{base}},
		{name: "an existing directory", dir: base},
		{
			name:       "a flush that fails",
			dir:        filepath.Join(base, "fails"),
			syncErr:    errSpy,
			wantErr:    errSpy,
			wantSynced: []string{base},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var synced []string

			opener := OSFS{syncDir: func(path string) error {
				synced = append(synced, path)

				return tt.syncErr
			}}

			root, err := opener.Open(tt.dir)

			assert.Equal(t, tt.wantSynced, synced)

			require.ErrorIs(t, err, tt.wantErr)

			if root != nil {
				require.NoError(t, root.Close())
			}

			info, statErr := os.Stat(tt.dir)
			require.NoError(t, statErr)
			assert.True(t, info.IsDir())
		})
	}
}

var errBusy = errors.New("flush busy")

// TestOSFS_AFailedFlushIsRememberedAndRetriedByTheNextOpen: the path exists after a failed flush, and that does not
// mean its entry is durable. The next Open flushes what is left (not what was done) until it works, and then no more.
func TestOSFS_AFailedFlushIsRememberedAndRetriedByTheNextOpen(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	dir := filepath.Join(base, "a", "state")

	var (
		synced []string
		fail   = map[string]error{filepath.Join(base, "a"): errBusy}
	)

	opener := NewOSFS()
	opener.syncDir = func(path string) error {
		synced = append(synced, path)

		return fail[path]
	}

	// the first open creates the path, flushes base, and fails on base/a
	_, err := opener.Open(dir)
	require.ErrorIs(t, err, ErrParentSync)
	require.ErrorIs(t, err, errBusy)
	assert.Equal(t, []string{base, filepath.Join(base, "a")}, synced)

	// the path exists now; the next open flushes only what was left, and fails again the same way
	synced = nil
	_, err = opener.Open(dir)
	require.ErrorIs(t, err, ErrParentSync)
	assert.Equal(t, []string{filepath.Join(base, "a")}, synced)

	// the flush works: the open succeeds, and the one after it has nothing left to flush
	synced, fail = nil, nil

	root, err := opener.Open(dir)
	require.NoError(t, err)
	require.NoError(t, root.Close())
	assert.Equal(t, []string{filepath.Join(base, "a")}, synced)

	synced = nil
	root, err = opener.Open(dir)
	require.NoError(t, err)
	require.NoError(t, root.Close())
	assert.Empty(t, synced)
}

// TestOSFS_APermissionErrorOnTheFlushSkipsIt: a parent that cannot be opened for the flush does not stop the open;
// the root reports the skip, and the other parents are still flushed.
func TestOSFS_APermissionErrorOnTheFlushSkipsIt(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	dir := filepath.Join(base, "a", "state")

	var synced []string

	opener := NewOSFS()
	opener.syncDir = func(path string) error {
		synced = append(synced, path)

		if path == base {
			return fs.ErrPermission
		}

		return nil
	}

	root, err := opener.Open(dir)
	require.NoError(t, err)

	defer func() { require.NoError(t, root.Close()) }()

	assert.Equal(t, []string{base, filepath.Join(base, "a")}, synced)

	reporter, ok := root.(interface{ ParentSync() string })
	require.True(t, ok)
	assert.Equal(t, "skipped", reporter.ParentSync())
}

// TestNew_RetriesAFailedParentFlushWithItsOwnOpener: the memory of a failed flush is the opener's, so New retries once
// with the opener it was given, which costs nothing. A flush that fails twice fails New.
func TestNew_RetriesAFailedParentFlushWithItsOwnOpener(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantErr    error
		name       string
		failures   int
		wantSynced int
	}{
		{name: "the flush works the second time", failures: 1, wantSynced: 2},
		{name: "the flush fails twice", failures: 2, wantErr: ErrParentSync, wantSynced: 2},
		{name: "the flush works", failures: 0, wantSynced: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var (
				synced  int
				opener  = NewOSFS()
				dirPath = filepath.Join(t.TempDir(), "state")
			)

			opener.syncDir = func(string) error {
				synced++
				if synced <= tt.failures {
					return errBusy
				}

				return nil
			}

			store, err := New(Config{FS: opener, Dir: dirPath})

			assert.Equal(t, tt.wantSynced, synced)

			require.ErrorIs(t, err, tt.wantErr)

			if store != nil {
				require.NoError(t, store.Close())
			}
		})
	}
}

var errNoFlush = errors.New("cannot flush a directory on this system")

// TestOSRoot_TheLayoutStepFollowsTheDirectoryFlushOfThePlatform: the layout step flushes the directory through the
// one function of the platform (flushDir). When the platform cannot flush a directory and says so with an error, the
// save fails and the status says io; with the function that does nothing, which is what Windows has, the same save
// succeeds. The caller is the same on every platform.
func TestOSRoot_TheLayoutStepFollowsTheDirectoryFlushOfThePlatform(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		flush      func(*os.File) error
		wantReason string
	}{
		{
			name:       "a platform that cannot flush a directory and says so",
			flush:      func(*os.File) error { return errNoFlush },
			wantReason: ReasonIO,
		},
		{name: "a platform where the flush does nothing", flush: func(*os.File) error { return nil }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			opener := NewOSFS()
			opener.flushDir = tt.flush
			opener.syncDir = func(string) error { return nil }

			store, err := New(Config{FS: opener, Dir: filepath.Join(t.TempDir(), "state")})
			require.NoError(t, err)

			defer func() { require.NoError(t, store.Close()) }()

			store.Hooks().Save(manifestreader.Persist{
				Snapshot: &manifestreader.Snapshot{}, ETag: "e", Manifest: []byte("m"),
			})

			res := store.write(t.Context(), store.pending)
			assert.Equal(t, tt.wantReason, res.reason)
		})
	}
}

// TestNew_AParentFlushThatWasSkippedStaysSkippedAcrossARetry: the fact that an outer parent could not be flushed for
// lack of permission is kept with the pending flushes, so a store that opens after a retry still reports it (and
// logs the warning once); a retry with no skip reports none.
func TestNew_AParentFlushThatWasSkippedStaysSkippedAcrossARetry(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		wantSync    string
		outer       []error
		inner       []error
		wantWarning int
	}{
		{
			name: "skipped, then an I/O error, then it works", outer: []error{fs.ErrPermission},
			inner: []error{errBusy, nil}, wantSync: "skipped", wantWarning: 1,
		},
		{
			name: "not skipped, an I/O error, then it works", outer: []error{nil},
			inner: []error{errBusy, nil}, wantSync: "",
		},
		{
			name: "skipped and nothing else", outer: []error{fs.ErrPermission}, inner: []error{nil},
			wantSync: "skipped", wantWarning: 1,
		},
		{name: "nothing wrong", outer: []error{nil}, inner: []error{nil}, wantSync: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			base := t.TempDir()
			dir := filepath.Join(base, "a", "state")
			answers := map[string][]error{base: tt.outer, filepath.Join(base, "a"): tt.inner}

			opener := NewOSFS()
			opener.syncDir = func(path string) error {
				list := answers[path]
				if len(list) == 0 {
					return nil
				}

				answers[path] = list[1:]

				return list[0]
			}

			var logs strings.Builder

			store, err := New(Config{FS: opener, Dir: dir, Logger: slog.New(slog.NewTextHandler(&logs, nil))})
			require.NoError(t, err, "New retries the flush once with its opener")

			defer func() { require.NoError(t, store.Close()) }()

			assert.Equal(t, tt.wantSync, store.Hooks().StateStatus().ParentSync)
			assert.Equal(t, tt.wantWarning, strings.Count(logs.String(), "level=WARN"))
		})
	}
}

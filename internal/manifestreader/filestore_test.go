package manifestreader_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/manifestreader"
)

const (
	pathDirA     = "dir/a.txt"
	fileA        = "a.txt"
	fileManifest = "manifest.yaml"

	contentInside = "inside"
)

// newFileStoreAt returns the store of dir.
func newFileStoreAt(t *testing.T, dir string) *manifestreader.FileStore {
	t.Helper()

	store, err := manifestreader.NewFileStore(dir)
	require.NoError(t, err)

	return store
}

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()

	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	}
}

// symlinkOrSkip makes a symbolic link; a platform that does not allow it (Windows without the privilege) skips
// the test with the reason.
func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()

	err := os.Symlink(target, link)
	if err != nil {
		t.Skipf("this platform does not allow symbolic links here: %v", err)
	}
}

// wantDenied asserts a refused read: reason denied, not retryable, with the given cause.
func wantDenied(t *testing.T, err, cause error) {
	t.Helper()

	var fetchErr *manifestreader.FetchError

	require.ErrorAs(t, err, &fetchErr)
	assert.Equal(t, manifestreader.ReasonDenied, fetchErr.Reason)
	assert.False(t, fetchErr.Retryable)
	assert.False(t, fetchErr.Throttled)
	assert.ErrorIs(t, err, cause)
}

func TestNewFileStore(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o600))

	tests := []struct {
		name    string
		root    string
		wantErr bool
	}{
		{name: "a directory", root: dir},
		{name: "empty", root: "", wantErr: true},
		{name: "missing", root: filepath.Join(dir, "missing"), wantErr: true},
		{name: "a file", root: file, wantErr: true},
		{name: "relative", root: "."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store, err := manifestreader.NewFileStore(tt.root)
			if tt.wantErr {
				require.ErrorIs(t, err, manifestreader.ErrInvalidConfig)
				assert.Nil(t, store)

				return
			}

			require.NoError(t, err)
			assert.NotNil(t, store)
		})
	}
}

func TestFileStore_ContainmentAndSymlinks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		setup   func(t *testing.T, base, root string)
		wantErr error
		name    string
		key     string
		want    string
		wantNF  bool
	}{
		{
			name: "a plain file", key: pathDirA, want: contentInside,
			setup: func(t *testing.T, _, root string) {
				t.Helper()

				writeTree(t, root, map[string]string{pathDirA: contentInside})
			},
		},
		{
			name: "a file link to a file inside is followed", key: "link.txt", want: contentInside,
			setup: func(t *testing.T, _, root string) {
				t.Helper()

				writeTree(t, root, map[string]string{pathDirA: contentInside})
				symlinkOrSkip(t, pathDirA, filepath.Join(root, "link.txt"))
			},
		},
		{
			name: "a link whose target climbs and stays inside is followed", key: "dir/up.txt", want: contentInside,
			setup: func(t *testing.T, _, root string) {
				t.Helper()

				writeTree(t, root, map[string]string{"other/a.txt": contentInside})
				require.NoError(t, os.MkdirAll(filepath.Join(root, "dir"), 0o750))
				symlinkOrSkip(t, "../other/a.txt", filepath.Join(root, "dir", "up.txt"))
			},
		},
		{
			name: "a directory link inside is followed", key: "dirlink/a.txt", want: contentInside,
			setup: func(t *testing.T, _, root string) {
				t.Helper()

				writeTree(t, root, map[string]string{"real/a.txt": contentInside})
				symlinkOrSkip(t, "real", filepath.Join(root, "dirlink"))
			},
		},
		{
			name: "a chain of links inside is followed", key: "one", want: contentInside,
			setup: func(t *testing.T, _, root string) {
				t.Helper()

				writeTree(t, root, map[string]string{"end.txt": contentInside})
				symlinkOrSkip(t, "two", filepath.Join(root, "one"))
				symlinkOrSkip(t, "three", filepath.Join(root, "two"))
				symlinkOrSkip(t, "end.txt", filepath.Join(root, "three"))
			},
		},
		{
			name: "a link to a file outside is refused", key: "escape.txt", wantErr: manifestreader.ErrOutsideRoot,
			setup: func(t *testing.T, base, root string) {
				t.Helper()

				writeTree(t, base, map[string]string{"secret.txt": "SECRET"})
				symlinkOrSkip(t, "../secret.txt", filepath.Join(root, "escape.txt"))
			},
		},
		{
			name:    "a link to a directory outside is refused",
			key:     "outdir/secret.txt",
			wantErr: manifestreader.ErrOutsideRoot,
			setup: func(t *testing.T, base, root string) {
				t.Helper()

				writeTree(t, base, map[string]string{"outside/secret.txt": "SECRET"})
				symlinkOrSkip(t, "../outside", filepath.Join(root, "outdir"))
			},
		},
		{
			name:    "an absolute link is refused even to a file inside",
			key:     "abs.txt",
			wantErr: manifestreader.ErrOutsideRoot,
			setup: func(t *testing.T, _, root string) {
				t.Helper()

				writeTree(t, root, map[string]string{fileA: contentInside})
				symlinkOrSkip(t, filepath.Join(root, fileA), filepath.Join(root, "abs.txt"))
			},
		},
		{
			name:    "a link that leaves and comes back is refused",
			key:     "again.txt",
			wantErr: manifestreader.ErrOutsideRoot,
			setup: func(t *testing.T, base, root string) {
				t.Helper()

				writeTree(t, root, map[string]string{fileA: contentInside})
				symlinkOrSkip(t, "../"+filepath.Base(root)+"/a.txt", filepath.Join(root, "again.txt"))

				_ = base
			},
		},
		{
			name: "a dangling link is a missing file", key: "dangling", wantNF: true,
			setup: func(t *testing.T, _, root string) {
				t.Helper()

				symlinkOrSkip(t, "nowhere", filepath.Join(root, "dangling"))
			},
		},
		{
			name: "a link loop is refused", key: "loop-a", wantErr: syscall.ELOOP,
			setup: func(t *testing.T, _, root string) {
				t.Helper()

				symlinkOrSkip(t, "loop-b", filepath.Join(root, "loop-a"))
				symlinkOrSkip(t, "loop-a", filepath.Join(root, "loop-b"))
			},
		},
		{
			name: "a file used as a directory is a missing file", key: "a.txt/child", wantNF: true,
			setup: func(t *testing.T, _, root string) {
				t.Helper()

				writeTree(t, root, map[string]string{fileA: contentInside})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			base := t.TempDir()
			root := filepath.Join(base, "root")
			require.NoError(t, os.Mkdir(root, 0o750))
			tt.setup(t, base, root)

			obj, err := newFileStoreAt(t, root).Get(t.Context(), manifestreader.GetRequest{Key: tt.key, Limit: 1 << 10})

			switch {
			case tt.wantNF:
				require.ErrorIs(t, err, manifestreader.ErrNotFound)
				assert.Equal(t, manifestreader.Object{}, obj)
			case tt.wantErr != nil:
				wantDenied(t, err, tt.wantErr)
				assert.Equal(t, manifestreader.Object{}, obj)
				assert.NotContains(t, string(obj.Bytes), "SECRET")
			default:
				require.NoError(t, err)
				assert.Equal(t, tt.want, string(obj.Bytes))
			}
		})
	}
}

func TestFileStore_VersionIDIsNotSupported(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeTree(t, dir, map[string]string{fileA: "x"})

	obj, err := newFileStoreAt(
		t,
		dir,
	).Get(t.Context(), manifestreader.GetRequest{Key: fileA, VersionID: "v1", Limit: 10})
	require.ErrorIs(t, err, manifestreader.ErrNotFound)
	assert.Equal(t, manifestreader.Object{}, obj)
}

func TestFileStore_DirectoryIsRefused(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"sub/a.txt": "x"})

	_, err := newFileStoreAt(t, dir).Get(t.Context(), manifestreader.GetRequest{Key: "sub", Limit: 10})
	wantDenied(t, err, manifestreader.ErrNotRegular)
}

func TestFileStore_RootThatDisappearsIsDenied(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "root")
	require.NoError(t, os.Mkdir(dir, 0o750))

	store := newFileStoreAt(t, dir)
	require.NoError(t, os.Remove(dir))

	_, err := store.Get(t.Context(), manifestreader.GetRequest{Key: fileA, Limit: 10})
	wantDenied(t, err, manifestreader.ErrRootUnavailable)
}

func TestFileStore_RootThatIsReplacedIsFollowed(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	root := filepath.Join(base, "root")
	writeTree(t, root, map[string]string{fileA: "first"})

	store := newFileStoreAt(t, root)
	assert.Equal(t, []byte("first"), mustGetFile(t, store, fileA, "").Bytes)

	// the whole directory is swapped, as a remounted volume is
	require.NoError(t, os.Rename(root, filepath.Join(base, "old")))
	writeTree(t, root, map[string]string{fileA: "second"})

	assert.Equal(t, []byte("second"), mustGetFile(t, store, fileA, "").Bytes)
}

func mustGetFile(t *testing.T, store manifestreader.ObjectStore, key, etag string) manifestreader.Object {
	t.Helper()

	obj, err := store.Get(t.Context(), manifestreader.GetRequest{Key: key, ETag: etag, Limit: 1 << 20})
	require.NoError(t, err)

	return obj
}

func TestFileStore_ETagIsTheQuotedSha256(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeTree(t, dir, map[string]string{fileA: "hello"})

	sum := sha256.Sum256([]byte("hello"))

	obj := mustGetFile(t, newFileStoreAt(t, dir), fileA, "")
	assert.Equal(t, `"`+hex.EncodeToString(sum[:])+`"`, obj.ETag)
}

// TestFileStore_SameSizeSameTimeRewriteIsSeen is the case a stat-based validator gets wrong: the content changes,
// the size, the modification time and the inode do not.
func TestFileStore_SameSizeSameTimeRewriteIsSeen(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, fileA)
	require.NoError(t, os.WriteFile(path, []byte("AAAA"), 0o600))

	before, err := os.Stat(path)
	require.NoError(t, err)

	store := newFileStoreAt(t, dir)
	first := mustGetFile(t, store, fileA, "")

	require.NoError(t, os.WriteFile(path, []byte("BBBB"), 0o600))
	require.NoError(t, os.Chtimes(path, before.ModTime(), before.ModTime()))

	after, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, before.Size(), after.Size())
	require.True(t, before.ModTime().Equal(after.ModTime()), "the modification time is the one of before")
	require.True(t, os.SameFile(before, after), "the same inode")

	second := mustGetFile(t, store, fileA, first.ETag)
	assert.Equal(t, []byte("BBBB"), second.Bytes)
	assert.NotEqual(t, first.ETag, second.ETag)
}

func TestFileStore_ConfigMapSwap(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	root := filepath.Join(base, "mount")

	// the layout kubelet writes: a timestamped directory, ..data pointing to it, and one link per key
	writeTree(
		t,
		root,
		map[string]string{"..2024_01_a/manifest.yaml": "generation one", "..2024_01_a/sub/obj.json": "{}"},
	)
	symlinkOrSkip(t, "..2024_01_a", filepath.Join(root, "..data"))
	symlinkOrSkip(t, "..data/manifest.yaml", filepath.Join(root, fileManifest))
	symlinkOrSkip(t, "..data/sub", filepath.Join(root, "sub"))

	store := newFileStoreAt(t, root)
	first := mustGetFile(t, store, fileManifest, "")
	assert.Equal(t, []byte("generation one"), first.Bytes)
	assert.Equal(t, []byte("{}"), mustGetFile(t, store, "sub/obj.json", "").Bytes, "a link to a directory")

	_, err := store.Get(t.Context(), manifestreader.GetRequest{Key: fileManifest, ETag: first.ETag, Limit: 100})
	require.ErrorIs(t, err, manifestreader.ErrNotModified)

	// the swap: a new directory, ..data_tmp pointing to it, renamed over ..data (atomic), the old directory removed
	writeTree(
		t,
		root,
		map[string]string{"..2024_01_b/manifest.yaml": "generation two", "..2024_01_b/sub/obj.json": "[]"},
	)
	symlinkOrSkip(t, "..2024_01_b", filepath.Join(root, "..data_tmp"))
	require.NoError(t, os.Rename(filepath.Join(root, "..data_tmp"), filepath.Join(root, "..data")))
	require.NoError(t, os.RemoveAll(filepath.Join(root, "..2024_01_a")))

	second := mustGetFile(t, store, fileManifest, first.ETag)
	assert.Equal(t, []byte("generation two"), second.Bytes)
	assert.NotEqual(t, first.ETag, second.ETag)
	assert.Equal(t, []byte("[]"), mustGetFile(t, store, "sub/obj.json", "").Bytes)

	_, err = store.Get(t.Context(), manifestreader.GetRequest{Key: fileManifest, ETag: second.ETag, Limit: 100})
	require.ErrorIs(t, err, manifestreader.ErrNotModified)

	// the data directory itself is a plain key
	assert.Equal(t, []byte("generation two"), mustGetFile(t, store, "..data/manifest.yaml", "").Bytes)
}

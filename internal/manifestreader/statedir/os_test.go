package statedir_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/manifestreader"
	"github.com/blackbirdworks/semaphore/internal/manifestreader/statedir"
)

func modeOf(tb testing.TB, name string) fs.FileMode {
	tb.Helper()

	info, err := os.Lstat(name)
	require.NoError(tb, err)

	return info.Mode()
}

func TestOS_RoundTripAndModes(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "nested", "state")

	run := openStore(t, statedir.NewOSFS(), statedir.Config{Dir: dir})
	run.saveAndFlush(persistOf(etagOne, manifestOne, contentAlpha, contentBeta))
	run.close()

	tests := []struct {
		name     string
		path     string
		wantMode fs.FileMode
	}{
		{"the directory is private", dir, fs.ModeDir | 0o700},
		{"the objects directory is private", filepath.Join(dir, "objects"), fs.ModeDir | 0o700},
		{"the manifest copy", filepath.Join(dir, manifestName), 0o600},
		{"the version file", filepath.Join(dir, versionName), 0o600},
		{"the lock file", filepath.Join(dir, "LOCK"), 0o600},
		{"an object", filepath.Join(dir, "objects", sumOf(contentAlpha)), 0o600},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.wantMode, modeOf(t, tt.path), tt.name)
	}

	entries, err := os.ReadDir(filepath.Join(dir, "objects"))
	require.NoError(t, err)
	assert.Len(t, entries, 2, "no temporary file is left behind")

	store, err := statedir.New(statedir.Config{Dir: dir})
	require.NoError(t, err)

	defer func() { require.NoError(t, store.Close()) }()

	restored, err := store.Hooks().Restore()
	require.NoError(t, err)
	assertRestored(
		t,
		wantRestored{etag: etagOne, manifest: manifestOne, objects: []string{contentAlpha, contentBeta}},
		restored,
	)
}

func TestOS_AcceptsOrRefusesAnExistingDirectoryByItsMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantErr  error
		name     string
		mode     fs.FileMode
		wantMode fs.FileMode
	}{
		{name: "private", mode: 0o700, wantMode: fs.ModeDir | 0o700},
		{name: "group-writable, as a Kubernetes fsGroup volume", mode: 0o770, wantMode: fs.ModeDir | 0o770},
		{name: "readable by everybody", mode: 0o755, wantMode: fs.ModeDir | 0o755},
		{name: "writable by everybody", mode: 0o777, wantErr: statedir.ErrUnsafe, wantMode: fs.ModeDir | 0o777},
		{name: "writable by others", mode: 0o702, wantErr: statedir.ErrUnsafe, wantMode: fs.ModeDir | 0o702},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := filepath.Join(t.TempDir(), "state")
			require.NoError(t, os.Mkdir(dir, 0o700))
			require.NoError(t, os.Chmod(dir, tt.mode))

			store, err := statedir.New(statedir.Config{Dir: dir})
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
				require.NoError(t, store.Close())
			}

			assert.Equal(t, tt.wantMode, modeOf(t, dir), "the mode of an existing directory is never changed")
		})
	}
}

func TestOS_RefusesWhatItCannotOwn(t *testing.T) {
	t.Parallel()

	tests := []struct {
		prepare func(t *testing.T, root string) string
		name    string
	}{
		{
			name: "the directory is a symlink",
			prepare: func(t *testing.T, root string) string {
				t.Helper()

				target := filepath.Join(root, "elsewhere")
				require.NoError(t, os.Mkdir(target, 0o700))
				require.NoError(t, os.Symlink(target, filepath.Join(root, "state")))

				return filepath.Join(root, "state")
			},
		},
		{
			name: "the path is a file",
			prepare: func(t *testing.T, root string) string {
				t.Helper()

				name := filepath.Join(root, "state")
				require.NoError(t, os.WriteFile(name, []byte("x"), 0o600))

				return name
			},
		},
		{
			name: "objects is a symlink out of the directory",
			prepare: func(t *testing.T, root string) string {
				t.Helper()

				target := filepath.Join(root, "elsewhere")
				dir := filepath.Join(root, "state")

				require.NoError(t, os.Mkdir(target, 0o700))
				require.NoError(t, os.Mkdir(dir, 0o700))
				require.NoError(t, os.Symlink(target, filepath.Join(dir, "objects")))

				return dir
			},
		},
		{
			name: "the manifest copy is a symlink",
			prepare: func(t *testing.T, root string) string {
				t.Helper()

				dir := filepath.Join(root, "state")
				require.NoError(t, os.Mkdir(dir, 0o700))
				require.NoError(t, os.Symlink(filepath.Join(root, "target"), filepath.Join(dir, manifestName)))

				return dir
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			dir := tt.prepare(t, root)

			_, err := statedir.New(statedir.Config{Dir: dir})
			require.ErrorIs(t, err, statedir.ErrUnsafe)

			// nothing was written through the link
			entries, readErr := os.ReadDir(filepath.Join(root, "elsewhere"))
			if readErr == nil {
				assert.Empty(t, entries)
			}
		})
	}
}

func TestOS_SecondProcessIsRefused(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "state")

	first, err := statedir.New(statedir.Config{Dir: dir})
	require.NoError(t, err)

	_, err = statedir.New(statedir.Config{Dir: dir})
	require.ErrorIs(t, err, statedir.ErrLocked)

	require.NoError(t, first.Close())

	second, err := statedir.New(statedir.Config{Dir: dir})
	require.NoError(t, err, "the lock is released with the directory")
	require.NoError(t, second.Close())
}

func TestOS_TornFilesAreIgnored(t *testing.T) {
	t.Parallel()

	tests := []struct {
		damage      func(t *testing.T, dir string)
		name        string
		wantRestore string
	}{
		{
			name: "manifest copy cut in half", wantRestore: manifestreader.RestoreEmpty,
			damage: func(t *testing.T, dir string) {
				t.Helper()

				name := filepath.Join(dir, manifestName)
				info, err := os.Stat(name)
				require.NoError(t, err)
				require.NoError(t, os.Truncate(name, info.Size()/2))
			},
		},
		{
			name: "object cut in half", wantRestore: manifestreader.RestorePartial,
			damage: func(t *testing.T, dir string) {
				t.Helper()

				require.NoError(t, os.Truncate(filepath.Join(dir, "objects", sumOf("alpha-content")), 3))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := filepath.Join(t.TempDir(), "state")
			run := openStore(t, statedir.NewOSFS(), statedir.Config{Dir: dir})
			run.saveAndFlush(persistOf(etagOne, manifestOne, "alpha-content"))
			run.close()

			tt.damage(t, dir)

			store, err := statedir.New(statedir.Config{Dir: dir})
			require.NoError(t, err)

			defer func() { require.NoError(t, store.Close()) }()

			_, _ = store.Hooks().Restore()
			assert.Equal(t, tt.wantRestore, store.Hooks().StateStatus().Restore)
		})
	}
}

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

// TestOS_ADirectoryWhereAFileBelongsDoesNotBlockTheSaves: a directory under the name of an object or of the manifest
// copy cannot be replaced by a rename. An empty one is removed and replaced; one with content is never removed, the
// object is left out of the save (a partial save) and the manifest copy is reported as unsafe.
func TestOS_ADirectoryWhereAFileBelongsDoesNotBlockTheSaves(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		wantSaveError string
		wantObjects   []string
		inManifest    bool
		withContent   bool
		wantPartial   bool
	}{
		{name: "an empty directory at an object", wantObjects: []string{contentAlpha, contentBeta}},
		{
			name: "a directory with content at an object", withContent: true, wantObjects: []string{contentAlpha},
			wantPartial: true,
		},
		{
			name:        "an empty directory at the manifest copy",
			inManifest:  true,
			wantObjects: []string{contentAlpha, contentBeta},
		},
		{
			name: "a directory with content at the manifest copy", inManifest: true, withContent: true,
			wantSaveError: statedir.ReasonUnsafe,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := filepath.Join(t.TempDir(), "state")
			persist := persistOf(etagOne, manifestOne, contentAlpha, contentBeta)

			first := openStore(t, statedir.NewOSFS(), statedir.Config{Dir: dir})
			first.saveAndFlush(persist)
			first.close()

			victim := filepath.Join(dir, "objects", sumOf(contentBeta))
			if tt.inManifest {
				victim = filepath.Join(dir, manifestName)
			}

			require.NoError(t, os.Remove(victim))
			require.NoError(t, os.Mkdir(victim, 0o700))

			if tt.withContent {
				require.NoError(t, os.WriteFile(filepath.Join(victim, "keep"), []byte("mine"), 0o600))
			}

			second := openStore(t, statedir.NewOSFS(), statedir.Config{Dir: dir})

			_, _ = second.store.Hooks().Restore()
			second.saveAndFlush(persist)

			status := second.store.Hooks().StateStatus()
			assert.Equal(t, tt.wantSaveError, status.LastSaveError)
			assert.Equal(t, tt.wantPartial, status.LastSavePartial)
			second.close()

			if tt.withContent {
				content, err := os.ReadFile(filepath.Join(victim, "keep"))
				require.NoError(t, err)
				assert.Equal(t, "mine", string(content), "a directory with content is never removed")

				if tt.wantSaveError != "" {
					return
				}
			}

			third, err := statedir.New(statedir.Config{Dir: dir})
			require.NoError(t, err)

			defer func() { require.NoError(t, third.Close()) }()

			restored, err := third.Hooks().Restore()
			require.NoError(t, err)
			assertRestored(t, wantRestored{etag: etagOne, manifest: manifestOne, objects: tt.wantObjects}, restored)
		})
	}
}

const outsideContent = "outside"

// TestOS_ALiveWriterNoticesANameThatChangedType: on a store that stays open (no restart), an object file that was
// replaced by a directory or a symbolic link is not trusted. An empty directory and a symbolic link are replaced by
// the file again (a rename replaces the link itself, never what it points to); a directory with content is never
// removed and the object is left out of the save.
func TestOS_ALiveWriterNoticesANameThatChangedType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		replace      func(t *testing.T, name, outside string)
		name         string
		wantOutside  string
		wantOnDisk   []string
		wantPartial  bool
		wantKeepFile bool
	}{
		{
			name: "an empty directory", wantOnDisk: []string{contentAlpha, contentBeta}, wantOutside: outsideContent,
			replace: func(t *testing.T, name, _ string) {
				t.Helper()
				require.NoError(t, os.Mkdir(name, 0o700))
			},
		},
		{
			name: "a directory with content", wantOnDisk: []string{contentAlpha}, wantPartial: true,
			wantOutside: outsideContent, wantKeepFile: true,
			replace: func(t *testing.T, name, _ string) {
				t.Helper()
				require.NoError(t, os.Mkdir(name, 0o700))
				require.NoError(t, os.WriteFile(filepath.Join(name, "keep"), []byte("mine"), 0o600))
			},
		},
		{
			name:        "a symbolic link to a file outside",
			wantOnDisk:  []string{contentAlpha, contentBeta},
			wantOutside: outsideContent,
			replace: func(t *testing.T, name, outside string) {
				t.Helper()
				require.NoError(t, os.Symlink(outside, name))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			dir := filepath.Join(root, "state")
			outside := filepath.Join(root, "outside.txt")
			require.NoError(t, os.WriteFile(outside, []byte(outsideContent), 0o600))

			run := openStore(t, statedir.NewOSFS(), statedir.Config{Dir: dir})
			run.saveAndFlush(persistOf(etagOne, manifestOne, contentAlpha, contentBeta))

			victim := filepath.Join(dir, "objects", sumOf(contentBeta))
			require.NoError(t, os.Remove(victim))
			tt.replace(t, victim, outside)

			// the same store, the same objects, a new manifest: nothing is reopened
			run.saveAndFlush(persistOf(etagTwo, manifestTwo, contentAlpha, contentBeta))

			status := run.store.Hooks().StateStatus()
			assert.Empty(t, status.LastSaveError)
			assert.Equal(t, tt.wantPartial, status.LastSavePartial)

			assert.ElementsMatch(t, tt.wantOnDisk, objectsOnDisk(t, dir, contentAlpha, contentBeta))

			untouched, err := os.ReadFile(outside)
			require.NoError(t, err)
			assert.Equal(t, tt.wantOutside, string(untouched), "what a link points to is never written")

			if tt.wantKeepFile {
				kept, keepErr := os.ReadFile(filepath.Join(victim, "keep"))
				require.NoError(t, keepErr)
				assert.Equal(t, "mine", string(kept), "a directory with content is never removed")
			}
		})
	}
}

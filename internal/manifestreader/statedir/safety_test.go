package statedir_test

import (
	"bytes"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/manifestreader"
	"github.com/blackbirdworks/semaphore/internal/manifestreader/statedir"
)

// image is every file of the disk with its content: two images are equal when nothing was added, changed or removed.
func image(disk *memDisk) map[string]string {
	out := map[string]string{}

	for _, name := range disk.names() {
		data, _ := disk.read(name)
		out[name] = string(data)
	}

	return out
}

func TestNew_NeverTouchesADirectoryThatIsNotOurs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		files   map[string]string
		wantErr error
		name    string
	}{
		{
			name: "files of somebody else and no VERSION",
			files: map[string]string{
				manifestName: "{}", ".tmp-user": "mine", "notes.txt": "keep me", "objects/" + sumOf("x"): "x",
			},
			wantErr: statedir.ErrUnsafe,
		},
		{
			name:    "one foreign file is enough",
			files:   map[string]string{"notes.txt": "keep me"},
			wantErr: statedir.ErrUnsafe,
		},
		{
			name: "a newer layout version, with files",
			files: map[string]string{
				versionName:  "semaphore-manifest-state 2\n",
				manifestName: "future format",
				"extra":      "x",
			},
			wantErr: statedir.ErrForeignVersion,
		},
		{
			name:    "a VERSION of another product",
			files:   map[string]string{versionName: "hello"},
			wantErr: statedir.ErrForeignVersion,
		},
		{name: "our own leftovers are fine", files: map[string]string{".tmp-12": "half"}},
		{name: "an empty directory is fine"},
		{
			name:  "a directory that is ours",
			files: map[string]string{versionName: "semaphore-manifest-state 1\n", "objects/" + sumOf("x"): "x"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			disk := newMemDisk()
			for name, content := range tt.files {
				disk.write(name, []byte(content))
			}

			before := image(disk)

			fsys := &memFS{disk: disk, faults: &faults{}}
			store, err := statedir.New(statedir.Config{FS: fsys, Dir: stateDirName})

			require.ErrorIs(t, err, tt.wantErr)
			assert.Equal(t, tt.wantErr == nil, store != nil)

			if store != nil {
				require.NoError(t, store.Close())
			}

			assert.Equal(t, before, image(disk), "nothing was written, changed or removed")
			assert.Zero(t, fsys.faults.steps, "not one mutating file operation")
		})
	}
}

func TestStore_ADirectoryThatBecomesForeignIsNotTouched(t *testing.T) {
	t.Parallel()

	disk := newMemDisk()
	run := openStore(t, &memFS{disk: disk, faults: &faults{}}, statedirConfig())
	run.saveAndFlush(persistOf(etagOne, manifestOne, contentAlpha))

	// somebody wipes our layout and puts their files there
	disk.remove(versionName)
	disk.removeObjectsDir()
	disk.write("notes.txt", []byte("keep me"))

	before := image(disk)

	run.saveAndFlush(persistOf(`"e2"`, "manifest-2", contentBeta))

	status := run.store.Hooks().StateStatus()
	assert.Equal(t, statedir.ReasonUnsafe, status.LastSaveError)
	assert.Equal(t, uint64(1), status.SaveErrors)
	assert.Equal(t, before, image(disk), "nothing was written, changed or removed")
}

func TestStore_GarbageCollectionRemovesOnlyNamesItWrote(t *testing.T) {
	t.Parallel()

	// garbage collection removes only what this package wrote: a checksum name or a temporary file
	disk := newMemDisk()
	run := openStore(t, &memFS{disk: disk, faults: &faults{}}, statedirConfig())
	run.saveAndFlush(persistOf(etagOne, manifestOne, contentAlpha))

	disk.write("objects/notes.txt", []byte(keepName))
	disk.write("objects/.tmp-user", []byte(keepName))
	disk.write("objects/"+strings.Repeat("A", 64), []byte("keep: not lower case hex"))
	disk.write("objects/.tmp-7", []byte("ours"))

	run.saveAndFlush(persistOf(`"e2"`, "manifest-2", contentBeta))

	assert.ElementsMatch(t, []string{
		manifestName,
		"objects/" + strings.Repeat("A", 64),
		objectFile(contentBeta),
		"objects/.tmp-user",
		"objects/notes.txt",
		versionName,
	}, disk.names())
}

// logBuffer collects the log lines of a test.
type logBuffer struct {
	buf bytes.Buffer
	mu  sync.Mutex
}

func (l *logBuffer) Write(data []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.buf.Write(data)
}

func (l *logBuffer) lines() []string {
	l.mu.Lock()
	defer l.mu.Unlock()

	return strings.Split(strings.TrimSpace(l.buf.String()), "\n")
}

func TestOpenOrDisable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		prepare         func(t *testing.T) statedir.Config
		name            string
		wantReason      string
		wantLogContains []string
		wantStatus      manifestreader.StateStatus
		wantStore       bool
	}{
		{
			name: "a usable directory", wantReason: "", wantStore: true,
			wantStatus: manifestreader.StateStatus{Enabled: true},
			prepare: func(*testing.T) statedir.Config {
				return statedir.Config{FS: &memFS{disk: newMemDisk(), faults: &faults{}}, Dir: stateDirName}
			},
		},
		{
			name:       "files of somebody else",
			wantReason: statedir.DisabledUnsafe,
			wantStatus: manifestreader.StateStatus{
				Restore:        manifestreader.RestoreDisabled,
				DisabledReason: statedir.DisabledUnsafe,
			},
			wantLogContains: []string{levelError, "reason=" + statedir.DisabledUnsafe},
			prepare: func(*testing.T) statedir.Config {
				disk := newMemDisk()
				disk.write("notes.txt", []byte("x"))

				return statedir.Config{FS: &memFS{disk: disk, faults: &faults{}}, Dir: stateDirName}
			},
		},
		{
			name:       "another layout version",
			wantReason: statedir.DisabledForeignVersion,
			wantStatus: manifestreader.StateStatus{
				Restore:        manifestreader.RestoreDisabled,
				DisabledReason: statedir.DisabledForeignVersion,
			},
			wantLogContains: []string{levelError, "reason=" + statedir.DisabledForeignVersion},
			prepare: func(*testing.T) statedir.Config {
				disk := newMemDisk()
				disk.write(versionName, []byte("semaphore-manifest-state 9\n"))

				return statedir.Config{FS: &memFS{disk: disk, faults: &faults{}}, Dir: stateDirName}
			},
		},
		{
			name:       "another process holds it",
			wantReason: statedir.DisabledLocked,
			wantStatus: manifestreader.StateStatus{
				Restore:        manifestreader.RestoreDisabled,
				DisabledReason: statedir.DisabledLocked,
			},
			wantLogContains: []string{levelError, "reason=" + statedir.DisabledLocked},
			prepare: func(t *testing.T) statedir.Config {
				t.Helper()

				fsys := &memFS{disk: newMemDisk(), faults: &faults{}}
				holder, err := statedir.New(statedir.Config{FS: fsys, Dir: stateDirName})
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, holder.Close()) })

				return statedir.Config{FS: fsys, Dir: stateDirName}
			},
		},
		{
			name:       "the system refuses access",
			wantReason: statedir.DisabledUnsafe,
			wantStatus: manifestreader.StateStatus{
				Restore:        manifestreader.RestoreDisabled,
				DisabledReason: statedir.DisabledUnsafe,
			},
			wantLogContains: []string{levelError, "reason=" + statedir.DisabledUnsafe},
			prepare: func(*testing.T) statedir.Config {
				return statedir.Config{FS: failingFS{err: fs.ErrPermission}, Dir: stateDirName}
			},
		},
		{
			name:       "any other failure",
			wantReason: statedir.DisabledError,
			wantStatus: manifestreader.StateStatus{
				Restore:        manifestreader.RestoreDisabled,
				DisabledReason: statedir.DisabledError,
			},
			wantLogContains: []string{levelError, "reason=" + statedir.DisabledError},
			prepare: func(*testing.T) statedir.Config {
				return statedir.Config{FS: failingFS{err: errNotAvailable}, Dir: stateDirName}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			logs := &logBuffer{}
			cfg := tt.prepare(t)
			cfg.Logger = slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

			store, hooks, reason := statedir.OpenOrDisable(cfg)
			assert.Equal(t, tt.wantReason, reason)
			require.NotNil(t, hooks.StateStatus, "the hooks are always there: the relay never fails to start")

			assert.Equal(t, tt.wantStore, store != nil)
			assert.Equal(t, tt.wantStore, hooks.Save != nil)
			assert.Equal(t, tt.wantStore, hooks.Restore != nil)
			assert.Equal(t, tt.wantStatus, hooks.StateStatus())

			lines := nonEmpty(logs.lines())
			assert.Len(t, lines, min(len(tt.wantLogContains), 1), "logged once, or not at all")

			for _, want := range tt.wantLogContains {
				assert.Contains(t, strings.Join(lines, "\n"), want)
			}

			if store != nil {
				require.NoError(t, store.Close())
			}
		})
	}
}

func nonEmpty(lines []string) []string {
	var out []string

	for _, line := range lines {
		if line != "" {
			out = append(out, line)
		}
	}

	return out
}

// treeImage is every entry of a real directory, with the content of the files: two images are equal when nothing was
// added, removed, changed or made into something else, a directory included.
func treeImage(tb testing.TB, dir string) map[string]string {
	tb.Helper()

	out := map[string]string{}

	require.NoError(tb, filepath.WalkDir(dir, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		switch {
		case entry.Type()&fs.ModeSymlink != 0:
			target, linkErr := os.Readlink(name)
			out[name] = "<link " + target + ">"

			return linkErr
		case entry.IsDir():
			info, infoErr := entry.Info()
			out[name] = "<dir " + info.Mode().String() + ">"

			return infoErr
		}

		data, readErr := os.ReadFile(name)
		out[name] = string(data)

		return readErr
	}))

	return out
}

// TestStore_OwnershipIsCheckedBeforeAnythingIsTouched: on every write the VERSION file is validated before anything is
// probed, removed or written. A VERSION that became foreign (or unreadable) between two saves stops the save
// untouched: the full directory listing and every content are the same before and after, including an empty
// directory under an object name that the write would otherwise have removed.
func TestStore_OwnershipIsCheckedBeforeAnythingIsTouched(t *testing.T) {
	t.Parallel()

	tests := []struct {
		damage func(t *testing.T, dir string)
		name   string
	}{
		{
			name: "another layout version",
			damage: func(t *testing.T, dir string) {
				t.Helper()
				require.NoError(
					t,
					os.WriteFile(filepath.Join(dir, versionName), []byte("semaphore-manifest-state 2\n"), 0o600),
				)
			},
		},
		{
			name: "a VERSION of another product",
			damage: func(t *testing.T, dir string) {
				t.Helper()
				require.NoError(t, os.WriteFile(filepath.Join(dir, versionName), []byte("hello"), 0o600))
			},
		},
		{
			name: "a VERSION that is a directory",
			damage: func(t *testing.T, dir string) {
				t.Helper()
				require.NoError(t, os.Remove(filepath.Join(dir, versionName)))
				require.NoError(t, os.Mkdir(filepath.Join(dir, versionName), 0o700))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := filepath.Join(t.TempDir(), "state")
			run := openStore(t, statedir.NewOSFS(), statedir.Config{Dir: dir})
			run.saveAndFlush(persistOf(etagOne, manifestOne, contentAlpha))

			tt.damage(t, dir)

			// an empty directory where the next write would put an object: it is removed by a write that is
			// allowed to touch the directory
			require.NoError(t, os.Mkdir(filepath.Join(dir, "objects", sumOf(contentBeta)), 0o700))

			before := treeImage(t, dir)

			run.saveAndFlush(persistOf(etagTwo, manifestTwo, contentAlpha, contentBeta))

			status := run.store.Hooks().StateStatus()
			assert.Equal(t, statedir.ReasonUnsafe, status.LastSaveError)
			assert.Equal(t, uint64(1), status.SaveErrors)
			assert.Equal(t, before, treeImage(t, dir), "nothing was added, changed or removed")
		})
	}
}

func TestStore_OwnershipIsCheckedAfterAReopenToo(t *testing.T) {
	t.Parallel()

	disk := newMemDisk()
	fault := &faults{}
	fsys := &memFS{disk: disk, faults: fault}
	run := openStore(t, fsys, statedirConfig())
	run.saveAndFlush(persistOf(etagOne, manifestOne, contentAlpha))

	// a failed write: the next write opens the directory again
	fault.reset()
	fault.failAt, fault.err = 1, syscall.EIO

	run.saveAndFlush(persistOf(etagTwo, manifestTwo, contentAlpha, contentBeta))
	require.Equal(t, uint64(1), run.store.Hooks().StateStatus().SaveErrors)

	// meanwhile the directory became somebody else's
	disk.write(versionName, []byte("semaphore-manifest-state 2\n"))

	before := image(disk)
	opens := fsys.opens.Load()

	run.saveAndFlush(persistOf(etagTwo, manifestTwo, contentAlpha, contentBeta))

	assert.Equal(t, opens+1, fsys.opens.Load(), "the directory was opened again")
	assert.Equal(t, statedir.ReasonUnsafe, run.store.Hooks().StateStatus().LastSaveError)
	assert.Equal(t, before, image(disk), "nothing was added, changed or removed")
}

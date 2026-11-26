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
				manifestName: "{}", ".tmp-user": "mine", notesName: "keep me", "objects/" + sumOf("x"): "x",
			},
			wantErr: statedir.ErrUnsafe,
		},
		{
			name:    "one foreign file is enough",
			files:   map[string]string{notesName: "keep me"},
			wantErr: statedir.ErrUnsafe,
		},
		{
			name: "a newer layout version, with files",
			files: map[string]string{
				versionName:  foreignLayout,
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
	disk.write(notesName, []byte("keep me"))

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

	disk.write(notesObject, []byte(keepName))
	disk.write(tmpUserObject, []byte(keepName))
	disk.write("objects/"+strings.Repeat("A", 64), []byte("keep: not lower case hex"))
	disk.write("objects/.tmp-7", []byte("ours"))

	run.saveAndFlush(persistOf(`"e2"`, "manifest-2", contentBeta))

	assert.ElementsMatch(t, []string{
		manifestName,
		"objects/" + strings.Repeat("A", 64),
		objectFile(contentBeta),
		tmpUserObject,
		notesObject,
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
				disk.write(notesName, []byte("x"))

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

// treeImage is every entry of a real directory: the type, the permissions (where the system has them), the content of
// each file and the target of each link: two images are equal when nothing was
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
			out[name] = "<dir " + modeNote(info.Mode()) + ">"

			return infoErr
		}

		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}

		data, readErr := os.ReadFile(name)
		out[name] = modeNote(info.Mode()) + string(data)

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
					os.WriteFile(filepath.Join(dir, versionName), []byte(foreignLayout), 0o600),
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
	disk.write(versionName, []byte(foreignLayout))

	before := image(disk)
	opens := fsys.opens.Load()

	run.saveAndFlush(persistOf(etagTwo, manifestTwo, contentAlpha, contentBeta))

	assert.Equal(t, opens+1, fsys.opens.Load(), "the directory was opened again")
	assert.Equal(t, statedir.ReasonUnsafe, run.store.Hooks().StateStatus().LastSaveError)
	assert.Equal(t, before, image(disk), "nothing was added, changed or removed")
}

// TestNew_ARefusalReleasesTheLockBeforeItRemovesTheFile: when a directory is refused, the lock is released first (its
// file is closed, which some systems need before the file can be removed), then the lock file is removed if this call
// created it, then the directory is closed. A lock file that was there before stays. A lock file that cannot be
// removed is logged and never replaces the reason of the refusal.
func TestNew_ARefusalReleasesTheLockBeforeItRemovesTheFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		files         map[string]string
		removeLockErr error
		wantErr       error
		name          string
		wantLog       []string
		wantOps       []string
	}{
		{
			name: "foreign files, no lock file before", files: map[string]string{notesName: "x"},
			wantErr: statedir.ErrUnsafe, wantOps: []string{opLock, opUnlock, opRemoveLock, opClose},
		},
		{
			name:    "a foreign version, no lock file before",
			files:   map[string]string{versionName: foreignLayout},
			wantErr: statedir.ErrForeignVersion,
			wantOps: []string{opLock, opUnlock, opRemoveLock, opClose},
		},
		{
			name: "a lock file that was there before stays", files: map[string]string{notesName: "x", "LOCK": ""},
			wantErr: statedir.ErrUnsafe, wantOps: []string{opLock, opUnlock, opClose},
		},
		{
			name: "a lock file that cannot be removed", files: map[string]string{notesName: "x"},
			removeLockErr: syscall.EBUSY, wantErr: statedir.ErrUnsafe,
			wantOps: []string{opLock, opUnlock, opRemoveLock, opClose},
			wantLog: []string{"level=WARN", "reason=lock-file"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			disk := newMemDisk()
			for name, content := range tt.files {
				disk.write(name, []byte(content))
			}

			logs := &logBuffer{}
			fsys := &memFS{disk: disk, faults: &faults{}, removeLockErr: tt.removeLockErr}
			store, err := statedir.New(statedir.Config{
				FS: fsys, Dir: stateDirName, Logger: slog.New(slog.NewTextHandler(logs, nil)),
			})

			require.ErrorIs(t, err, tt.wantErr, "the reason of the refusal is never masked")
			assert.Nil(t, store)
			assert.Equal(t, tt.wantOps, fsys.takeLockOps(), "the order of the lock operations")

			lines := nonEmpty(logs.lines())
			assert.Len(t, lines, min(len(tt.wantLog), 1))

			for _, want := range tt.wantLog {
				assert.Contains(t, strings.Join(lines, "\n"), want)
			}
		})
	}
}

// TestStore_ATakenTemporaryNameIsReplaced: a temporary file is created exclusively under a random name; a name that
// is taken is replaced by another, a bounded number of times.
func TestStore_ATakenTemporaryNameIsReplaced(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		wantLastSaveError string
		wantSaveErrors    uint64
		taken             int32
	}{
		{name: "no name is taken", taken: 0},
		{name: "three names are taken", taken: 3},
		{name: "every attempt but the last", taken: 7},
		{name: "every attempt is taken", taken: 8, wantLastSaveError: statedir.ReasonIO, wantSaveErrors: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fsys := &memFS{disk: newMemDisk(), faults: &faults{}}
			run := openStore(t, fsys, statedirConfig())

			fsys.existFirst.Store(tt.taken)
			run.saveAndFlush(persistOf(etagOne, manifestOne, contentAlpha))

			status := run.store.Hooks().StateStatus()
			assert.Equal(t, tt.wantLastSaveError, status.LastSaveError)
			assert.Equal(t, tt.wantSaveErrors, status.SaveErrors)
		})
	}
}

// TestStore_LeftoverTemporaryFilesAreRemovedOnlyWhereTheLockIsHeld: a crashed writer leaves temporary files, named
// ".tmp-" and hex. Where the lock gives this store the directory they are stale and are removed by pattern; without
// a lock another writer may be in the middle of a write, so they are not ours and stay. Foreign names stay always.
func TestStore_LeftoverTemporaryFilesAreRemovedOnlyWhereTheLockIsHeld(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		wantNames []string
		noLock    bool
	}{
		{
			name: "with a lock",
			wantNames: []string{
				versionName,
				manifestName,
				objectFile(contentAlpha),
				tmpUserObject,
				notesObject,
			},
		},
		{
			name: "without a lock", noLock: true,
			wantNames: []string{
				versionName, manifestName, objectFile(contentAlpha), tmpUserObject, notesObject,
				"objects/.tmp-0badc0ffee", ".tmp-0badc0ffee",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			disk := newMemDisk()
			fsys := &memFS{disk: disk, faults: &faults{}, noLock: tt.noLock}
			run := openStore(t, fsys, statedirConfig())
			run.saveAndFlush(persistOf(etagOne, manifestOne, contentAlpha))

			disk.write("objects/.tmp-0badc0ffee", []byte("half"))
			disk.write(".tmp-0badc0ffee", []byte("half"))
			disk.write(tmpUserObject, []byte("mine"))
			disk.write(notesObject, []byte("mine"))

			run.saveAndFlush(persistOf(etagTwo, manifestTwo, contentAlpha))

			assert.ElementsMatch(t, tt.wantNames, disk.names())
		})
	}
}

// TestNew_AVersionThatAppearsWhileTheDirectoryIsInspectedIsRead: on a system without a lock a second writer can create
// the layout between the read of VERSION and the listing of the directory. The listing then shows its files, and
// the directory is not mistaken for a foreign one: VERSION is read again.
func TestNew_AVersionThatAppearsWhileTheDirectoryIsInspectedIsRead(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantErr error
		appears string
		name    string
	}{
		{name: "the version of this layout", appears: "semaphore-manifest-state 1\n"},
		{name: "a foreign version", appears: foreignLayout, wantErr: statedir.ErrForeignVersion},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			disk := newMemDisk()
			fsys := &memFS{disk: disk, faults: &faults{}, noLock: true}
			fsys.beforeList = func() {
				disk.write(versionName, []byte(tt.appears))
				disk.write("objects/"+sumOf(contentAlpha), []byte(contentAlpha))
			}

			store, err := statedir.New(statedir.Config{FS: fsys, Dir: stateDirName})

			require.ErrorIs(t, err, tt.wantErr)
			assert.Equal(t, tt.wantErr == nil, store != nil)

			if store != nil {
				require.NoError(t, store.Close())
			}
		})
	}
}

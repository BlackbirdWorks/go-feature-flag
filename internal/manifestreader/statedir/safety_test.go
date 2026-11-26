package statedir_test

import (
	"bytes"
	"io/fs"
	"log/slog"
	"strings"
	"sync"
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

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, store)
			} else {
				require.NoError(t, err)
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

	disk.write("objects/notes.txt", []byte("keep"))
	disk.write("objects/.tmp-user", []byte("keep"))
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
		prepare    func(t *testing.T) statedir.Config
		name       string
		wantReason string
	}{
		{
			name: "a usable directory", wantReason: "",
			prepare: func(*testing.T) statedir.Config {
				return statedir.Config{FS: &memFS{disk: newMemDisk(), faults: &faults{}}, Dir: stateDirName}
			},
		},
		{
			name: "files of somebody else", wantReason: statedir.DisabledUnsafe,
			prepare: func(*testing.T) statedir.Config {
				disk := newMemDisk()
				disk.write("notes.txt", []byte("x"))

				return statedir.Config{FS: &memFS{disk: disk, faults: &faults{}}, Dir: stateDirName}
			},
		},
		{
			name: "another layout version", wantReason: statedir.DisabledForeignVersion,
			prepare: func(*testing.T) statedir.Config {
				disk := newMemDisk()
				disk.write(versionName, []byte("semaphore-manifest-state 9\n"))

				return statedir.Config{FS: &memFS{disk: disk, faults: &faults{}}, Dir: stateDirName}
			},
		},
		{
			name: "another process holds it", wantReason: statedir.DisabledLocked,
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
			name: "the system refuses access", wantReason: statedir.DisabledUnsafe,
			prepare: func(*testing.T) statedir.Config {
				return statedir.Config{FS: failingFS{err: fs.ErrPermission}, Dir: stateDirName}
			},
		},
		{
			name: "any other failure", wantReason: statedir.DisabledError,
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

			status := hooks.StateStatus()

			if tt.wantReason == "" {
				require.NotNil(t, store)
				assert.NotNil(t, hooks.Save)
				assert.NotNil(t, hooks.Restore)
				assert.True(t, status.Enabled)
				assert.Equal(t, []string{""}, logs.lines(), "nothing is logged")
				require.NoError(t, store.Close())

				return
			}

			assert.Nil(t, store)
			assert.Nil(t, hooks.Save)
			assert.Nil(t, hooks.Restore)
			assert.Equal(t, manifestreader.StateStatus{
				Restore: manifestreader.RestoreDisabled, DisabledReason: tt.wantReason,
			}, status)

			lines := logs.lines()
			require.Len(t, lines, 1, "logged once")
			assert.Contains(t, lines[0], "level=ERROR")
			assert.Contains(t, lines[0], "reason="+tt.wantReason)
		})
	}
}

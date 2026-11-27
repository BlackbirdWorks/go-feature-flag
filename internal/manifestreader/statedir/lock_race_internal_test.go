//go:build unix

package statedir

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOSRoot_ARefusedOpenerNeverLeavesAnotherOneLockingAnUnlinkedFile: the first opener takes the lock, finds the
// directory changed between its two inspections, is refused, and removes the lock file while still holding the lock.
// A second opener had the same lock file open before it was removed, and gets the lock after the first one released
// it. That lock is on a file that is no longer at the path: the second opener notices (the file it locked is not the
// file at the path) and takes the one that is there, and the lock operation itself tells whether its attempt made
// that file. The order is made by the hooks of the roots, not by timing.
func TestOSRoot_ARefusedOpenerNeverLeavesAnotherOneLockingAnUnlinkedFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantSecondErr  error
		name           string
		wantNames      []string
		foreignStays   bool
		recreateLock   bool
		wantLockAtPath bool
	}{
		{
			name:           "the foreign file goes away: the second opener gets the directory on the file at the path",
			wantNames:      []string{lockFile},
			wantLockAtPath: true,
		},
		{
			name:          "the foreign file stays: the lock file the retry made is removed, the foreign file is untouched",
			foreignStays:  true,
			wantSecondErr: ErrUnsafe,
			wantNames:     []string{"notes.txt"},
		},
		{
			name: "a lock file was there for the retry: it stays", foreignStays: true, recreateLock: true,
			wantSecondErr: ErrUnsafe, wantNames: []string{lockFile, "notes.txt"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			runLockRace(t, tt.foreignStays, tt.recreateLock, tt.wantSecondErr, tt.wantNames, tt.wantLockAtPath)
		})
	}
}

func runLockRace(
	t *testing.T, foreignStays, recreateLock bool, wantSecondErr error, wantNames []string, wantLockAtPath bool,
) {
	t.Helper()

	const bound = 10 * time.Second

	dir := filepath.Join(t.TempDir(), "state")
	require.NoError(t, os.Mkdir(dir, 0o700))

	var (
		firstLocked   = make(chan struct{})
		secondOpened  = make(chan struct{})
		firstReleased = make(chan struct{})
		foreign       = filepath.Join(dir, "notes.txt")
		hookErrs      = make(chan error, 4) // the hooks run on other goroutines: their errors are checked at the end
	)

	wait := func(ch <-chan struct{}) {
		select {
		case <-ch:
		case <-time.After(bound):
			assert.Fail(t, "a step of the arrangement did not happen")
		}
	}

	first := NewOSFS()
	first.afterLock = func() {
		close(firstLocked)
		wait(secondOpened)
		// the directory changes between the two inspections of the first opener: it is refused
		hookErrs <- os.WriteFile(foreign, []byte("x"), 0o600)
	}
	first.afterRelease = func() {
		if !foreignStays {
			hookErrs <- os.Remove(foreign)
		}

		if recreateLock {
			hookErrs <- os.WriteFile(filepath.Join(dir, lockFile), nil, 0o600)
		}

		close(firstReleased)
	}

	second := NewOSFS()

	var opened bool

	second.beforeFlock = func() {
		if opened {
			return
		}

		opened = true

		// the second opener has the lock file open, and waits until the first one released it
		close(secondOpened)
		wait(firstReleased)
	}

	type result struct {
		store *Store
		err   error
	}

	firstDone := make(chan result, 1)
	secondDone := make(chan result, 1)

	go func() {
		store, err := New(Config{FS: first, Dir: dir})
		firstDone <- result{store, err}
	}()

	wait(firstLocked)

	go func() {
		store, err := New(Config{FS: second, Dir: dir})
		secondDone <- result{store, err}
	}()

	got := [2]result{await(t, firstDone), await(t, secondDone)}

	close(hookErrs)

	for err := range hookErrs {
		require.NoError(t, err)
	}

	slices.Sort(wantNames)
	require.ErrorIs(t, got[0].err, ErrUnsafe, "the first opener was refused")

	if wantSecondErr != nil {
		require.ErrorIs(t, got[1].err, wantSecondErr)
	} else {
		require.NoError(t, got[1].err)
	}

	if got[1].store != nil {
		defer func() { require.NoError(t, got[1].store.Close()) }()
	}

	assert.Equal(t, wantNames, sortedNames(t, dir), "what is left in the directory")

	if foreignStays {
		content, err := os.ReadFile(foreign)
		require.NoError(t, err)
		assert.Equal(t, "x", string(content), "the foreign file is byte-identical")
	}

	if wantLockAtPath {
		assertLockAtPath(t, got[1].store, dir)
	}
}

func sortedNames(t *testing.T, dir string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}

	slices.Sort(names)

	return names
}

// assertLockAtPath: the lock of the opener is on the file at the path.
func assertLockAtPath(t *testing.T, store *Store, dir string) {
	t.Helper()

	root, ok := store.root.(*osRoot)
	require.True(t, ok)
	require.NotNil(t, root.lock)

	held, err := root.lock.Stat()
	require.NoError(t, err)

	named, err := os.Lstat(filepath.Join(dir, lockFile))
	require.NoError(t, err, "there is a lock file at the path")
	assert.True(t, os.SameFile(held, named), "the lock is on the file that is at the path, not on an unlinked one")
}

// await receives the result of an opener, failing the test when it does not come.
func await[T any](t *testing.T, ch <-chan T) T { //nolint:ireturn // a generic helper of the test
	t.Helper()

	select {
	case value := <-ch:
		return value
	case <-time.After(10 * time.Second):
		require.FailNow(t, "an opener did not return")
	}

	var zero T

	return zero
}

//go:build unix

package statedir

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOSRoot_ARefusedOpenerNeverLeavesAnotherOneLockingAnUnlinkedFile: the first opener takes the lock, finds the
// directory changed between its two inspections, is refused, and removes the lock file while still holding the lock.
// A second opener had the same lock file open before it was removed, and gets the lock after the first one released
// it. That lock is on a file that is no longer at the path: the second opener must notice (the file it locked is not
// the file at the path) and lock the one that is there. The order is made by the hooks of the roots, not by timing.
func TestOSRoot_ARefusedOpenerNeverLeavesAnotherOneLockingAnUnlinkedFile(t *testing.T) {
	t.Parallel()

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
		// the change is gone again when the second opener looks
		hookErrs <- os.Remove(foreign)

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

	var got [2]result

	select {
	case got[0] = <-firstDone:
	case <-time.After(bound):
		require.FailNow(t, "the first opener did not return")
	}

	select {
	case got[1] = <-secondDone:
	case <-time.After(bound):
		require.FailNow(t, "the second opener did not return")
	}

	close(hookErrs)

	for err := range hookErrs {
		require.NoError(t, err)
	}

	require.ErrorIs(t, got[0].err, ErrUnsafe, "the first opener was refused")
	require.NoError(t, got[1].err, "the second opener got the directory")

	defer func() { require.NoError(t, got[1].store.Close()) }()

	// the lock of the second opener is on the file at the path
	root, ok := got[1].store.root.(*osRoot)
	require.True(t, ok)
	require.NotNil(t, root.lock)

	held, err := root.lock.Stat()
	require.NoError(t, err)

	named, err := os.Lstat(filepath.Join(dir, "LOCK"))
	require.NoError(t, err, "there is a lock file at the path")
	assert.True(t, os.SameFile(held, named), "the lock is on the file that is at the path, not on an unlinked one")
}

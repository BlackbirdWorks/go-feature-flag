//go:build unix

package statedir_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/manifestreader"
	"github.com/blackbirdworks/semaphore/internal/manifestreader/statedir"
)

// TestOS_AParentThatCannotBeReadDoesNotDisableThePersistence: the state directory is a cache. A parent with write and
// search permission but no read permission lets the relay create the directory but not flush the parent: the
// directory is used, and the status says that the flush was skipped. (A process that runs as root can open every
// directory, so the case cannot be made there.)
func TestOS_AParentThatCannotBeReadDoesNotDisableThePersistence(t *testing.T) {
	t.Parallel()

	if os.Geteuid() == 0 {
		t.Skip("root can open a directory without read permission")
	}

	parent := filepath.Join(t.TempDir(), "cache")
	require.NoError(t, os.Mkdir(parent, 0o700))
	require.NoError(t, os.Chmod(parent, 0o300))

	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })

	dir := filepath.Join(parent, "state")

	store, hooks, reason := statedir.OpenOrDisable(statedir.Config{Dir: dir})
	require.Empty(t, reason)
	require.NotNil(t, store)

	assert.Equal(t, manifestreader.ParentSyncSkipped, hooks.StateStatus().ParentSync)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})

	go func() {
		store.Run(ctx)
		close(done)
	}()

	hooks.Save(persistOf(etagOne, manifestOne, contentAlpha))
	require.NoError(t, store.Flush(ctx))

	assert.Empty(t, hooks.StateStatus().LastSaveError)
	assert.Equal(t, uint64(1), hooks.StateStatus().Saves)

	cancel()

	select {
	case <-done:
	case <-time.After(wait):
		require.FailNow(t, "the writer did not stop")
	}

	require.NoError(t, store.Close())

	again, err := statedir.New(statedir.Config{Dir: dir})
	require.NoError(t, err)

	defer func() { require.NoError(t, again.Close()) }()

	restored, err := again.Hooks().Restore()
	require.NoError(t, err)
	assertRestored(t, wantRestored{etag: etagOne, manifest: manifestOne, objects: []string{contentAlpha}}, restored)
}

// TestOS_OwnershipIsCheckedAfterAForcedReopen: a write fails (the objects directory cannot be written), so the next
// write opens the directory again. Meanwhile the VERSION became foreign. The reopened directory is validated
// before the destinations are probed: an empty directory under an object name, which an allowed write would
// remove, is still there, and the full tree is the same before and after. (Skipped as root: root can write to a
// directory without permission.)
func TestOS_OwnershipIsCheckedAfterAForcedReopen(t *testing.T) {
	t.Parallel()

	if os.Geteuid() == 0 {
		t.Skip("root can write to a directory without write permission")
	}

	dir := filepath.Join(t.TempDir(), "state")
	run := openStore(t, statedir.NewOSFS(), statedir.Config{Dir: dir})
	run.saveAndFlush(persistOf(etagOne, manifestOne, contentAlpha))

	objects := filepath.Join(dir, "objects")
	require.NoError(t, os.Chmod(objects, 0o500))

	t.Cleanup(func() { _ = os.Chmod(objects, 0o700) })

	run.saveAndFlush(persistOf(etagTwo, manifestTwo, contentAlpha, contentBeta))
	require.Equal(t, uint64(1), run.store.Hooks().StateStatus().SaveErrors, "the write fails")

	require.NoError(t, os.Chmod(objects, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, versionName), []byte("semaphore-manifest-state 2\n"), 0o600))
	require.NoError(t, os.Mkdir(filepath.Join(objects, sumOf(contentBeta)), 0o700))

	before := treeImage(t, dir)

	run.saveAndFlush(persistOf(etagTwo, manifestTwo, contentAlpha, contentBeta))

	assert.Equal(t, statedir.ReasonUnsafe, run.store.Hooks().StateStatus().LastSaveError)
	assert.Equal(t, before, treeImage(t, dir))
}

package statedir_test

import (
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStore_RebuildsWhatVanishedWhileRunning(t *testing.T) {
	t.Parallel()

	tests := []struct {
		vanish func(*memDisk)
		name   string
	}{
		{name: "the objects directory", vanish: func(d *memDisk) { d.removeObjectsDir() }},
		{
			name: "the whole directory",
			vanish: func(d *memDisk) {
				for _, name := range d.names() {
					d.remove(name)
				}

				d.removeObjectsDir()
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			disk := newMemDisk()
			fsys := &memFS{disk: disk, faults: &faults{}}
			run := openStore(t, fsys, statedirConfig())
			run.saveAndFlush(persistOf(etagOne, manifestOne, contentAlpha, contentShared))

			tt.vanish(disk)

			// the next save rebuilds the layout and writes every object again: the one that did not change too
			run.saveAndFlush(persistOf(`"e2"`, "manifest-2", contentBeta, contentShared))

			status := run.store.Hooks().StateStatus()
			assert.Empty(t, status.LastSaveError)
			assert.Zero(t, status.SaveErrors)
			assert.Equal(t, uint64(2), status.Saves)
			run.close()

			restored, _, err := restoreOf(t, fsys)
			require.NoError(t, err)
			assertRestored(t, wantRestored{
				etag: `"e2"`, manifest: "manifest-2", objects: []string{contentBeta, contentShared},
			}, restored)
		})
	}
}

func TestStore_DoesNotTrustItsMemoryAfterAFailedSave(t *testing.T) {
	t.Parallel()

	disk := newMemDisk()
	fault := &faults{}
	fsys := &memFS{disk: disk, faults: fault}
	run := openStore(t, fsys, statedirConfig())
	stateB := persistOf(`"eB"`, "manifest-B", contentAlpha, contentShared)

	run.saveAndFlush(stateB)

	// the objects are lost behind our back, and then a save of another state fails
	disk.removeObjectsDir()

	fault.reset()
	fault.failAt, fault.err = 1, syscall.EIO

	run.saveAndFlush(persistOf(`"eC"`, "manifest-C", contentBeta))
	require.Equal(t, uint64(1), run.store.Hooks().StateStatus().SaveErrors)

	// the same state as the first save again: its digest is the one the store wrote before, and what it believed
	// about the disk is wrong. It must write everything again.
	run.saveAndFlush(stateB)
	run.close()

	assert.Equal(t, uint64(3), run.store.Hooks().StateStatus().Saves, "three writes: ok, failed, ok")

	restored, status, err := restoreOf(t, fsys)
	require.NoError(t, err)
	assert.Equal(t, 0, status.DiscardedObjects)
	assertRestored(t, wantRestored{
		etag: `"eB"`, manifest: "manifest-B", objects: []string{contentAlpha, contentShared},
	}, restored)
}

func TestStore_ARestartRewritesAnObjectFileThatWentMissing(t *testing.T) {
	t.Parallel()

	disk := newMemDisk()
	fsys := &memFS{disk: disk, faults: &faults{}}
	persist := persistOf(etagOne, manifestOne, contentAlpha, contentBeta)

	first := openStore(t, fsys, statedirConfig())
	first.saveAndFlush(persist)
	first.close()

	disk.remove(objectFile(contentBeta))

	second := openStore(t, fsys, statedirConfig())

	restored, err := second.store.Hooks().Restore()
	require.NoError(t, err)
	assertRestored(t, wantRestored{etag: etagOne, manifest: manifestOne, objects: []string{contentAlpha}}, restored)
	assert.Equal(t, 1, second.store.Hooks().StateStatus().DiscardedObjects)

	// the reader saves the same state again once it fetched the object: the file is back
	second.saveAndFlush(persist)
	second.close()

	_, there := disk.read(objectFile(contentBeta))
	assert.True(t, there)

	restored, status, err := restoreOf(t, fsys)
	require.NoError(t, err)
	assert.Zero(t, status.DiscardedObjects)
	assertRestored(
		t,
		wantRestored{etag: etagOne, manifest: manifestOne, objects: []string{contentAlpha, contentBeta}},
		restored,
	)
}

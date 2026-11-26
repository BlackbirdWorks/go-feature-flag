package statedir_test

import (
	"io/fs"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/manifestreader"
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
			run.saveAndFlush(persistOf(etagTwo, manifestTwo, contentBeta, contentShared))

			status := run.store.Hooks().StateStatus()
			assert.Empty(t, status.LastSaveError)
			assert.Zero(t, status.SaveErrors)
			assert.Equal(t, uint64(2), status.Saves)
			run.close()

			restored, _, err := restoreOf(t, fsys)
			require.NoError(t, err)
			assertRestored(t, wantRestored{
				etag: etagTwo, manifest: manifestTwo, objects: []string{contentBeta, contentShared},
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
	stateB := persistOf(etagB, manifestB, contentAlpha, contentShared)

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
		etag: etagB, manifest: manifestB, objects: []string{contentAlpha, contentShared},
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

func TestStore_ASaveThatAddsNoChecksumStillRebuildsWhatVanished(t *testing.T) {
	t.Parallel()

	changed := persistOf(etagTwo, manifestTwo, contentAlpha, contentBeta)
	unchanged := persistOf(etagOne, manifestOne, contentAlpha, contentBeta)

	tests := []struct {
		vanish       func(*memDisk)
		second       manifestreader.Persist
		name         string
		wantRestored wantRestored
	}{
		{
			name: "the objects directory", vanish: func(d *memDisk) { d.removeObjectsDir() }, second: changed,
			wantRestored: wantRestored{
				etag: etagTwo, manifest: manifestTwo, objects: []string{contentAlpha, contentBeta},
			},
		},
		{
			name: "one object file", vanish: func(d *memDisk) { d.remove(objectFile(contentAlpha)) }, second: changed,
			wantRestored: wantRestored{
				etag: etagTwo, manifest: manifestTwo, objects: []string{contentAlpha, contentBeta},
			},
		},
		{
			name: "the manifest copy", vanish: func(d *memDisk) { d.remove(manifestName) }, second: changed,
			wantRestored: wantRestored{
				etag: etagTwo, manifest: manifestTwo, objects: []string{contentAlpha, contentBeta},
			},
		},
		{
			name: "an unchanged state with a vanished object file", second: unchanged,
			vanish: func(d *memDisk) { d.remove(objectFile(contentBeta)) },
			wantRestored: wantRestored{
				etag: etagOne, manifest: manifestOne, objects: []string{contentAlpha, contentBeta},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			disk := newMemDisk()
			fsys := &memFS{disk: disk, faults: &faults{}}
			run := openStore(t, fsys, statedirConfig())
			run.saveAndFlush(unchanged)

			tt.vanish(disk)

			// the next write keeps every checksum, and so writes no new object by the book
			run.saveAndFlush(tt.second)
			assert.Empty(t, run.store.Hooks().StateStatus().LastSaveError)
			run.close()

			restored, status, err := restoreOf(t, fsys)
			require.NoError(t, err)
			assert.Zero(t, status.DiscardedObjects)
			assertRestore(t, tt.wantRestored, nil, restored, err)
		})
	}
}

func TestStore_ALostDirectoryIsOpenedAgainByALaterSave(t *testing.T) {
	t.Parallel()

	disk := newMemDisk()
	probe := &faults{}
	p := openStore(t, &memFS{disk: newMemDisk(), faults: probe}, statedirConfig())
	p.saveAndFlush(persistOf(etagB, manifestB, contentBeta))
	p.close()

	// the first write operation that touches the directory finds it gone, and opening it again fails once
	fault := &faults{failAt: stepOf(t, probe.ops, "create objects/", 1), err: fs.ErrNotExist}
	fsys := &memFS{disk: disk, faults: fault}
	run := openStore(t, fsys, statedirConfig())

	fsys.failOpens.Store(1)
	run.saveAndFlush(persistOf(etagB, manifestB, contentBeta))

	status := run.store.Hooks().StateStatus()
	assert.Equal(t, uint64(1), status.SaveErrors, "the directory could not be opened again")

	// the directory is usable again: the next save opens it and writes everything, though the state is the same
	run.saveAndFlush(persistOf(etagB, manifestB, contentBeta))

	status = run.store.Hooks().StateStatus()
	assert.Equal(t, uint64(1), status.SaveErrors)
	assert.Empty(t, status.LastSaveError)
	run.close()

	restored, _, err := restoreOf(t, &memFS{disk: disk, faults: &faults{}})
	require.NoError(t, err)
	assertRestored(t, wantRestored{etag: etagB, manifest: manifestB, objects: []string{contentBeta}}, restored)
}

// TestStore_AnyFailedWriteMakesTheNextWriteOpenTheDirectoryAgain: after a failed write, whatever the error, the next
// write opens the directory again (a new lock, the symbolic links checked again) before it trusts anything. A
// successful write does not.
func TestStore_AnyFailedWriteMakesTheNextWriteOpenTheDirectoryAgain(t *testing.T) {
	t.Parallel()

	probe := &faults{}
	p := openStore(t, &memFS{disk: newMemDisk(), faults: probe}, statedirConfig())
	p.saveAndFlush(persistOf(etagB, manifestB, contentBeta))
	p.close()

	tests := []struct {
		name      string
		err       error
		wantOpens []int32
	}{
		{name: "an I/O error", err: syscall.EIO, wantOpens: []int32{1, 2, 2}},
		{name: "a full disk", err: syscall.ENOSPC, wantOpens: []int32{1, 2, 2}},
		{name: "a permission error", err: fs.ErrPermission, wantOpens: []int32{1, 2, 2}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fault := &faults{failAt: stepOf(t, probe.ops, "create objects/", 1), err: tt.err}
			fsys := &memFS{disk: newMemDisk(), faults: fault}
			run := openStore(t, fsys, statedirConfig())

			opens := make([]int32, 0, 3)

			// the failing write, the write after it, and a write after a good one
			for _, persist := range []manifestreader.Persist{
				persistOf(etagB, manifestB, contentBeta), persistOf(etagB, manifestB, contentBeta),
				persistOf(etagTwo, manifestTwo, contentBeta),
			} {
				run.saveAndFlush(persist)

				opens = append(opens, fsys.opens.Load())
			}

			assert.Equal(t, tt.wantOpens, opens)
			assert.Equal(t, uint64(1), run.store.Hooks().StateStatus().SaveErrors)
		})
	}
}

// TestStore_AFailedWriteMakesTheStoreWriteEverythingAgain: after a write that failed, nothing that is only believed
// about the disk is trusted. An object file that was damaged without changing its name or type (so that a listing
// finds nothing wrong) is written again by the retry, though the store had written it before and would otherwise
// skip it.
func TestStore_AFailedWriteMakesTheStoreWriteEverythingAgain(t *testing.T) {
	t.Parallel()

	first := persistOf(etagOne, manifestOne, contentAlpha, contentBeta)
	second := persistOf(etagTwo, manifestTwo, contentAlpha, contentBeta)

	probe := &faults{}
	probing := openStore(t, &memFS{disk: newMemDisk(), faults: probe}, statedirConfig())
	probing.saveAndFlush(first)
	probe.reset()
	probing.saveAndFlush(second)
	probing.close()

	disk := newMemDisk()
	fault := &faults{}
	fsys := &memFS{disk: disk, faults: fault}
	run := openStore(t, fsys, statedirConfig())
	run.saveAndFlush(first)

	// the object file is damaged in place, and the next write fails before it is done
	disk.write(objectFile(contentBeta), []byte("damaged"))

	fault.reset()
	fault.failAt, fault.err = stepOf(t, probe.ops, opRenameMan, 1), syscall.EIO

	run.saveAndFlush(second)
	require.Equal(t, uint64(1), run.store.Hooks().StateStatus().SaveErrors)

	run.saveAndFlush(second)
	assert.Empty(t, run.store.Hooks().StateStatus().LastSaveError)
	run.close()

	restored, status, err := restoreOf(t, fsys)
	assertRestore(t, wantRestored{
		etag: etagTwo, manifest: manifestTwo, objects: []string{contentAlpha, contentBeta},
	}, nil, restored, err)
	assert.Zero(t, status.DiscardedObjects, "the damaged object was written again")
}

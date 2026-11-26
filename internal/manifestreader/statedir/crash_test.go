package statedir_test

import (
	"fmt"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/manifestreader"
)

// firstStep finds the first operation that starts with prefix.
func firstStep(prefix string) func(*testing.T, []string) int {
	return func(t *testing.T, ops []string) int {
		t.Helper()

		return stepOf(t, ops, prefix, 1)
	}
}

// onManifestFile finds the last operation that starts with prefix before the rename of the manifest: the one on the
// temporary file of the manifest.
func onManifestFile(prefix string) func(*testing.T, []string) int {
	return func(t *testing.T, ops []string) int {
		t.Helper()

		return stepBefore(t, ops, prefix, stepOf(t, ops, opRenameMan, 1))
	}
}

// stepBefore is the 1-based step of the last operation that starts with prefix before step limit.
func stepBefore(tb testing.TB, ops []string, prefix string, limit int) int {
	tb.Helper()

	for i := limit - 2; i >= 0; i-- {
		if strings.HasPrefix(ops[i], prefix) {
			return i + 1
		}
	}

	require.FailNow(tb, "operation not found", prefix)

	return 0
}

// stepOf is the 1-based step of the first operation that starts with prefix, at or after from (1-based).
func stepOf(tb testing.TB, ops []string, prefix string, from int) int {
	tb.Helper()

	for i := from - 1; i < len(ops); i++ {
		if strings.HasPrefix(ops[i], prefix) {
			return i + 1
		}
	}

	require.FailNow(tb, "operation not found", prefix)

	return 0
}

// TestStore_CrashAtEveryStep stops the process at each file operation of a save in turn (every operation after
// the failing one fails too, and a failing write is torn: half of it reaches the file) and checks what the next
// start finds: the old state before the rename of the manifest, the new one after it, nothing else. With powerLoss
// the disk also forgets everything that was not made durable (file data that was not synced, a directory entry that
// was not synced): the old state is then found until the directory sync that follows the rename of the manifest,
// the new one after it. A file with unsynced data, or an entry of a directory that was not synced, is never
// mistaken for valid content: it fails its digest or its checksum and is ignored.
func TestStore_CrashAtEveryStep(t *testing.T) {
	t.Parallel()

	stateA := persistOf(`"eA"`, "manifest-A", "a-only", contentShared)
	stateB := persistOf(etagB, manifestB, "b-only", contentShared)
	wantA := wantRestored{etag: `"eA"`, manifest: "manifest-A", objects: []string{"a-only", contentShared}}
	wantB := wantRestored{etag: etagB, manifest: manifestB, objects: []string{"b-only", contentShared}}

	deathNewFrom := func(manifestRename, _ int) int { return manifestRename }
	lossNewFrom := func(_, syncRoot int) int { return syncRoot }
	keep := func(d *memDisk) *memDisk { return d }
	lose := func(d *memDisk) *memDisk { return d.powerLoss() }

	tests := []crashMode{
		{name: "process death/old state", survive: keep, newFrom: deathNewFrom, old: &stateA, wantOld: wantA},
		{
			name: "process death/no old state", survive: keep, newFrom: deathNewFrom,
			wantOldErr: manifestreader.ErrNothingRestored,
		},
		{name: "power loss/old state", survive: lose, newFrom: lossNewFrom, old: &stateA, wantOld: wantA},
		{
			name: "power loss/no old state", survive: lose, newFrom: lossNewFrom,
			wantOldErr: manifestreader.ErrNothingRestored,
		},
	}

	for i := range tests {
		runCrashTable(t, &tests[i], stateB, wantB)
	}
}

// crashMode is one way of crashing: what the disk keeps (the process died, or the power was cut), when the new state
// is found, and what was saved before.
type crashMode struct {
	survive func(*memDisk) *memDisk
	// newFrom is the first step from which the new state is found, given the steps of the rename of the manifest and
	// of the flush of the directory after it
	newFrom    func(manifestRename, syncRoot int) int
	old        *manifestreader.Persist
	wantOldErr error
	name       string
	wantOld    wantRestored
}

func runCrashTable(
	t *testing.T, mode *crashMode, stateB manifestreader.Persist, wantB wantRestored,
) {
	t.Helper()

	// a clean run records the steps
	probe := &faults{}
	probeDisk := newMemDisk()

	if mode.old != nil {
		first := openStore(t, &memFS{disk: probeDisk, faults: &faults{}}, statedirConfig())
		first.saveAndFlush(*mode.old)
		first.close()
	}

	second := openStore(t, &memFS{disk: probeDisk, faults: probe}, statedirConfig())
	second.saveAndFlush(stateB)
	second.close()

	ops := slices.Clone(probe.ops)
	total := len(ops)
	manifestRename := stepOf(t, ops, opRenameMan, 1)
	syncRoot := stepOf(t, ops, "syncdir .", manifestRename)
	lastFatal := syncRoot
	newFrom := mode.newFrom(manifestRename, syncRoot)

	require.GreaterOrEqual(t, total, lastFatal, "the garbage collection comes after the manifest is durable")

	for step := 1; step <= total+1; step++ {
		t.Run(fmt.Sprintf("%s/step=%d", mode.name, step), func(t *testing.T) {
			t.Parallel()

			disk := newMemDisk()

			if mode.old != nil {
				clean := openStore(t, &memFS{disk: disk, faults: &faults{}}, statedirConfig())
				clean.saveAndFlush(*mode.old)
				clean.close()
			}

			crashing := &faults{failAt: step, crash: true, err: syscall.EIO}
			run := openStore(t, &memFS{disk: disk, faults: crashing}, statedirConfig())
			run.saveAndFlush(stateB)

			status := run.store.Hooks().StateStatus()
			run.close()

			// the save reports the failure of every step up to the durable manifest; the garbage collection
			// after it is best effort
			assert.Equal(t, step <= lastFatal, status.SaveErrors == 1, "reported failure")

			disk = mode.survive(disk)

			restored, _, err := restoreOf(t, &memFS{disk: disk, faults: &faults{}})

			wantErr, want := mode.wantOldErr, mode.wantOld
			if step > newFrom {
				wantErr, want = nil, wantB
			}

			require.ErrorIs(t, err, wantErr)
			assert.Equal(t, want.sorted(), summaryOf(restored))

			// after the crash the next save cleans up: exactly the files of the new state are left
			again := openStore(t, &memFS{disk: disk, faults: &faults{}}, statedirConfig())
			again.saveAndFlush(stateB)
			again.close()

			assert.Equal(t,
				[]string{versionName, manifestName, objectFile("b-only"), objectFile(contentShared)}, disk.names())
		})
	}
}

func TestStore_SaveFailuresAreReportedAndTheNextSaveRecovers(t *testing.T) {
	t.Parallel()

	stateB := persistOf(etagB, manifestB, "b1", "b2")
	stateC := persistOf(`"eC"`, "manifest-C", "c1", "b2")

	tests := []struct {
		err        error
		failStep   func(t *testing.T, ops []string) int
		name       string
		wantReason string
	}{
		{
			name:       "disk full on write",
			failStep:   firstStep("write objects/"),
			err:        syscall.ENOSPC,
			wantReason: "no-space",
		},
		{name: "disk full on create", failStep: firstStep(opCreateObj), err: syscall.ENOSPC, wantReason: "no-space"},
		{
			name:       "EIO on write of the manifest",
			failStep:   onManifestFile("write .tmp-"),
			err:        syscall.EIO,
			wantReason: "io",
		},
		{name: "fsync of an object fails", failStep: firstStep("sync objects/"), err: syscall.EIO, wantReason: "io"},
		{
			name:       "fsync of the manifest fails",
			failStep:   onManifestFile("sync .tmp-"),
			err:        syscall.EIO,
			wantReason: "io",
		},
		{name: "rename of the manifest fails", failStep: firstStep(opRenameMan), err: syscall.EIO, wantReason: "io"},
		{
			name:       "fsync of the directory fails",
			failStep:   firstStep("syncdir objects"),
			err:        syscall.EIO,
			wantReason: "io",
		},
		{name: "read-only file system", failStep: firstStep(opCreateObj), err: syscall.EROFS, wantReason: "denied"},
		{
			name:       "permission denied",
			failStep:   onManifestFile("create .tmp-"),
			err:        syscall.EACCES,
			wantReason: "denied",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// a probe finds the step of the operation
			probe := &faults{}
			p := openStore(t, &memFS{disk: newMemDisk(), faults: probe}, statedirConfig())
			p.saveAndFlush(stateB)
			p.close()

			// only that step fails; the operations after it work
			disk := newMemDisk()
			fault := &faults{failAt: tt.failStep(t, probe.ops), err: tt.err}
			run := openStore(t, &memFS{disk: disk, faults: fault}, statedirConfig())

			run.saveAndFlush(stateB)

			status := run.store.Hooks().StateStatus()
			assert.Equal(t, uint64(1), status.Saves)
			assert.Equal(t, uint64(1), status.SaveErrors)
			assert.Equal(t, tt.wantReason, status.LastSaveError)

			// polling goes on: the next snapshot is saved and the state is exactly that one
			run.saveAndFlush(stateC)

			status = run.store.Hooks().StateStatus()
			assert.Equal(t, uint64(2), status.Saves)
			assert.Equal(t, uint64(1), status.SaveErrors)
			assert.Empty(t, status.LastSaveError)
			assert.False(t, status.Pending)

			run.close()

			restored, _, err := restoreOf(t, &memFS{disk: disk, faults: &faults{}})
			require.NoError(t, err)
			assertRestored(
				t,
				wantRestored{etag: `"eC"`, manifest: "manifest-C", objects: []string{"c1", "b2"}},
				restored,
			)
		})
	}
}

// TestStore_SaveOperationOrder asserts the order of the file operations of a save, which is the whole crash-safety
// argument: every file is flushed before its rename; the objects directory is flushed after the last object rename
// and before the manifest is written; the manifest is flushed, renamed, and the directory flushed after it; the
// garbage collection comes last.
func TestStore_SaveOperationOrder(t *testing.T) {
	t.Parallel()

	disk := newMemDisk()

	first := openStore(t, &memFS{disk: disk, faults: &faults{}}, statedirConfig())
	first.saveAndFlush(persistOf(`"eA"`, "manifest-A", "a-only", contentShared))
	first.close()

	probe := &faults{}
	second := openStore(t, &memFS{disk: disk, faults: probe}, statedirConfig())

	_, err := second.store.Hooks().Restore()
	require.NoError(t, err)

	second.saveAndFlush(persistOf(etagB, manifestB, "b-only", contentShared))
	second.close()

	ops := probe.ops

	// every rename of a file comes right after the flush and the close of that file
	renames := 0

	for i, op := range ops {
		if !strings.HasPrefix(op, "rename ") {
			continue
		}

		renames++

		require.GreaterOrEqual(t, i, 2)
		assert.True(
			t,
			strings.HasPrefix(ops[i-2], "sync "),
			"the file is flushed before it is renamed: %q",
			ops[i-2:i+1],
		)
		assert.True(t, strings.HasPrefix(ops[i-1], "close "), "and closed: %q", ops[i-2:i+1])
	}

	assert.Equal(t, 2, renames, "one object and the manifest")

	lastObject := stepBefore(t, ops, "rename objects/", stepOf(t, ops, opRenameMan, 1))
	syncObjects := stepOf(t, ops, "syncdir objects", lastObject)
	manifestSync := stepOf(t, ops, "sync .tmp-", syncObjects)
	manifestRename := stepOf(t, ops, opRenameMan, 1)
	syncRoot := stepOf(t, ops, "syncdir .", manifestRename)
	collect := stepOf(t, ops, "remove objects/"+sumOf("a-only"), syncRoot)

	assert.Less(t, lastObject, syncObjects, "the objects directory is flushed after the last object is renamed")
	assert.Less(t, syncObjects, manifestSync, "and before the manifest is written")
	assert.Less(t, manifestSync, manifestRename)
	assert.Less(t, manifestRename, syncRoot, "the directory is flushed after the manifest is renamed")
	assert.Less(t, syncRoot, collect, "the garbage collection comes after the new manifest is durable")

	// nothing but removals follows the first removal of the collection
	for _, op := range ops[collect-1:] {
		assert.True(
			t,
			strings.HasPrefix(op, "remove ") || strings.HasPrefix(op, "syncdir objects"),
			"after the collection: %s",
			op,
		)
	}
}

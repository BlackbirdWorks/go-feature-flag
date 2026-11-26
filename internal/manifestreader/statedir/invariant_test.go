package statedir_test

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/manifestreader"
	"github.com/blackbirdworks/semaphore/internal/manifestreader/statedir"
)

// objectsOnDisk is the set of the object files of a state directory, by content (the contents given).
func objectsOnDisk(tb testing.TB, dir string, contents ...string) []string {
	tb.Helper()

	var present []string

	for _, content := range contents {
		info, err := os.Lstat(filepath.Join(dir, "objects", sumOf(content)))
		if err == nil && info.Mode().IsRegular() {
			present = append(present, content)
		}
	}

	return present
}

// TestStore_ASaveNeverRemovesAnObjectTheSnapshotStillNeeds is the invariant of the budget and of the blocked
// destinations: after a save, successful or partial, every object that the new snapshot references, that was on disk
// before, and that the budget allows, is still on disk. A destination that cannot be written (a directory with
// content under the name) is left out before the budget is applied, so it never takes the place of an object that
// could be kept.
func TestStore_ASaveNeverRemovesAnObjectTheSnapshotStillNeeds(t *testing.T) {
	t.Parallel()

	big := strings.Repeat("X", 10)
	small := strings.Repeat("Y", 4)
	mid := strings.Repeat("Z", 6)

	tests := []struct {
		name        string
		first       manifestreader.Persist
		second      manifestreader.Persist
		blocked     []string
		wantOnDisk  []string
		wantKept    []string
		budget      int64
		wantPartial bool
	}{
		{
			name: "a blocked object takes the last slot of the budget: the old object stays", budget: 10,
			first:  persistOf(etagOne, manifestOne, big),
			second: persistOf(etagTwo, manifestTwo, big, small), blocked: []string{small},
			wantOnDisk: []string{big}, wantPartial: true, wantKept: []string{big},
		},
		{
			name: "over the budget the objects on disk are kept first", budget: 10,
			first:      persistOf(etagOne, manifestOne, big),
			second:     persistOf(etagTwo, manifestTwo, big, small),
			wantOnDisk: []string{big}, wantPartial: true, wantKept: []string{big},
		},
		{
			name: "a blocked object is left out, the others are saved", budget: 100,
			first:  persistOf(etagOne, manifestOne, big, small),
			second: persistOf(etagTwo, manifestTwo, big, small, mid), blocked: []string{mid},
			wantOnDisk: []string{big, small}, wantPartial: true, wantKept: []string{big, small},
		},
		{
			name: "everything fits and nothing is blocked", budget: 100,
			first:      persistOf(etagOne, manifestOne, big),
			second:     persistOf(etagTwo, manifestTwo, big, small, mid),
			wantOnDisk: []string{big, small, mid}, wantKept: []string{big},
		},
		{
			name: "a stale object that fits is kept next to the current one", budget: 14,
			first:      persistOf(etagOne, manifestOne, big),
			second:     withStale(persistOf(etagTwo, manifestTwo, big), small),
			wantOnDisk: []string{big, small}, wantKept: []string{big},
		},
		{
			name: "a blocked stale object is left out and the save is not partial", budget: 100,
			first:  persistOf(etagOne, manifestOne, big),
			second: withStale(persistOf(etagTwo, manifestTwo, big), small), blocked: []string{small},
			wantOnDisk: []string{big}, wantKept: []string{big},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := filepath.Join(t.TempDir(), "state")
			run := openStore(t, statedir.NewOSFS(), statedir.Config{Dir: dir, MaxBytes: tt.budget})
			run.saveAndFlush(tt.first)

			for _, content := range tt.blocked {
				name := filepath.Join(dir, "objects", sumOf(content))
				require.NoError(t, os.MkdirAll(name, 0o700))
				require.NoError(t, os.WriteFile(filepath.Join(name, "keep"), []byte("mine"), 0o600))
			}

			run.saveAndFlush(tt.second)

			status := run.store.Hooks().StateStatus()
			assert.Empty(t, status.LastSaveError)
			assert.Equal(t, tt.wantPartial, status.LastSavePartial)

			all := []string{big, small, mid}
			assert.ElementsMatch(t, tt.wantOnDisk, objectsOnDisk(t, dir, all...))

			for _, content := range tt.wantKept {
				assert.Contains(t, objectsOnDisk(t, dir, all...), content, "needed by the snapshot, on disk before")
			}

			for _, content := range tt.blocked {
				kept, err := os.ReadFile(filepath.Join(dir, "objects", sumOf(content), "keep"))
				require.NoError(t, err)
				assert.Equal(t, "mine", string(kept), "a directory with content is never removed")
			}

			run.close()

			// and the manifest copy names exactly what is on disk: a restart restores it
			store, err := statedir.New(statedir.Config{Dir: dir, MaxBytes: tt.budget})
			require.NoError(t, err)

			defer func() { require.NoError(t, store.Close()) }()

			restored, err := store.Hooks().Restore()
			require.NoError(t, err)
			assert.Zero(t, store.Hooks().StateStatus().DiscardedObjects)
			assert.Len(t, restored.Objects, len(tt.wantOnDisk))
		})
	}
}

// TestStore_AFailedWriteDoesNotChangeWhatTheBudgetKeeps: a write that fails forgets which files are known to be
// durable (so they are written again), but not which files are there: the budget priority is taken from the
// directory, so the retry of the same snapshot keeps the object that is on disk and needed, exactly as an
// uninterrupted save would. Without that, the retry of an over-budget snapshot prefers the smaller new object and
// removes the one that was there.
func TestStore_AFailedWriteDoesNotChangeWhatTheBudgetKeeps(t *testing.T) {
	t.Parallel()

	big := strings.Repeat("X", 10)
	small := strings.Repeat("Y", 4)
	mid := strings.Repeat("Z", 6)

	tests := []struct {
		name       string
		failOp     string
		first      manifestreader.Persist
		second     manifestreader.Persist
		wantOnDisk []string
		budget     int64
	}{
		{
			name: "the manifest write fails, the retry is over budget", budget: 10,
			first:  persistOf(etagOne, manifestOne, big),
			second: persistOf(etagTwo, manifestTwo, big, small), failOp: opRenameMan,
			wantOnDisk: []string{big},
		},
		{
			name: "the write of a new object fails, the retry fits", budget: 10,
			first:  persistOf(etagOne, manifestOne, mid),
			second: persistOf(etagTwo, manifestTwo, mid, small), failOp: opCreateObj,
			wantOnDisk: []string{mid, small},
		},
		{
			name: "the manifest write fails, the retry is over budget with a bigger new object", budget: 10,
			first:  persistOf(etagOne, manifestOne, mid),
			second: persistOf(etagTwo, manifestTwo, mid, big), failOp: opRenameMan,
			wantOnDisk: []string{mid},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// the steps of the second save, in an uninterrupted run
			probe := &faults{}
			probing := openStore(t, &memFS{disk: newMemDisk(), faults: probe}, statedir.Config{MaxBytes: tt.budget})
			probing.saveAndFlush(tt.first)
			probe.reset()
			probing.saveAndFlush(tt.second)

			probing.close()

			failAt := stepOf(t, probe.ops, tt.failOp, 1)

			disk := newMemDisk()
			fault := &faults{}
			run := openStore(t, &memFS{disk: disk, faults: fault}, statedir.Config{MaxBytes: tt.budget})
			run.saveAndFlush(tt.first)

			fault.reset()
			fault.failAt, fault.err = failAt, syscall.EIO

			run.saveAndFlush(tt.second)
			require.Equal(t, uint64(1), run.store.Hooks().StateStatus().SaveErrors, "the second save fails")

			// the retry of the same snapshot
			run.saveAndFlush(tt.second)

			status := run.store.Hooks().StateStatus()
			assert.Empty(t, status.LastSaveError)
			assert.Equal(t, uint64(1), status.SaveErrors)

			var onDisk []string

			for _, content := range []string{big, small, mid} {
				if _, there := disk.read(objectFile(content)); there {
					onDisk = append(onDisk, content)
				}
			}

			assert.ElementsMatch(t, tt.wantOnDisk, onDisk)
			run.close()

			restored, _, err := restoreOf(t, &memFS{disk: disk, faults: &faults{}})
			require.NoError(t, err)
			assertRestored(t, wantRestored{etag: etagTwo, manifest: manifestTwo, objects: tt.wantOnDisk}, restored)
		})
	}
}

package statedir_test

import (
	"os"
	"path/filepath"
	"strings"
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

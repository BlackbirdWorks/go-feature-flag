package statedir_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/internal/manifestreader"
	"github.com/blackbirdworks/semaphore/internal/manifestreader/statedir"
)

func TestStore_RoundTrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		wantRestore wantRestored
		persist     manifestreader.Persist
		wantStatus  manifestreader.StateStatus
	}{
		{
			name:        "no objects: only the manifest",
			persist:     persistOf(`"e0"`, "manifest-0"),
			wantRestore: wantRestored{etag: `"e0"`, manifest: "manifest-0"},
			wantStatus:  manifestreader.StateStatus{Enabled: true, Restore: manifestreader.RestoreRestored},
		},
		{
			name:    "ok objects, two names with one content are one object",
			persist: persistOf(etagOne, manifestOne, contentAlpha, contentBeta, contentAlpha),
			wantRestore: wantRestored{
				etag:     etagOne,
				manifest: manifestOne,
				objects:  []string{contentAlpha, contentBeta},
			},
			wantStatus: manifestreader.StateStatus{
				Enabled: true, Restore: manifestreader.RestoreRestored, RestoredObjects: 2,
			},
		},
		{
			name:    "stale content is kept, an object that never loaded is not saved",
			persist: withMissing(withStale(persistOf(`"e2"`, "manifest-2", contentAlpha), "old-beta")),
			wantRestore: wantRestored{
				etag:     `"e2"`,
				manifest: "manifest-2",
				objects:  []string{contentAlpha, "old-beta"},
			},
			wantStatus: manifestreader.StateStatus{
				Enabled: true, Restore: manifestreader.RestoreRestored, RestoredObjects: 2,
			},
		},
		{
			name:        "a stale content that equals an ok one is saved once",
			persist:     withStale(persistOf(`"e3"`, "manifest-3", contentAlpha), contentAlpha),
			wantRestore: wantRestored{etag: `"e3"`, manifest: "manifest-3", objects: []string{contentAlpha}},
			wantStatus: manifestreader.StateStatus{
				Enabled: true, Restore: manifestreader.RestoreRestored, RestoredObjects: 1,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			disk := newMemDisk()
			fsys := &memFS{disk: disk, faults: &faults{}}

			first := openStore(t, fsys, statedirConfig())
			first.saveAndFlush(tt.persist)
			first.close()

			restored, status, err := restoreOf(t, fsys)
			require.NoError(t, err)
			assertRestored(t, tt.wantRestore, restored)
			assert.Equal(t, tt.wantStatus, status)
		})
	}
}

func TestStore_WritesNothingWhenNothingChanged(t *testing.T) {
	t.Parallel()

	fsys := &memFS{disk: newMemDisk(), faults: &faults{}}
	run := openStore(t, fsys, statedirConfig())
	persist := persistOf(etagOne, manifestOne, contentAlpha, contentBeta)

	run.saveAndFlush(persist)

	first := fsys.faults.steps
	require.Positive(t, first)

	status := run.store.Hooks().StateStatus()
	assert.Equal(t, uint64(1), status.Saves)
	assert.Equal(t, fixedNow(), status.LastSave)

	// the same snapshot again (the reader saves after every published snapshot): no file operation at all
	run.saveAndFlush(persist)

	assert.Equal(t, first, fsys.faults.steps, "no file operation for an unchanged state")
	assert.Equal(t, uint64(1), run.store.Hooks().StateStatus().Saves)
}

func TestStore_RestoreOfAnEmptyDirectory(t *testing.T) {
	t.Parallel()

	fsys := &memFS{disk: newMemDisk(), faults: &faults{}}

	restored, status, err := restoreOf(t, fsys)
	require.ErrorIs(t, err, manifestreader.ErrNothingRestored)
	assert.Nil(t, restored)
	assert.Equal(t, manifestreader.StateStatus{Enabled: true, Restore: manifestreader.RestoreEmpty}, status)
}

func TestStore_StaleNamesAreSavedWithTheEntryTheyAreServedFrom(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		wantStale []manifestreader.StaleObject
		cfg       statedir.Config
	}{
		{
			name: "the stale name and its content come back",
			wantStale: []manifestreader.StaleObject{{
				Name: "stale-0", Kind: "flags", Format: "json", Checksum: controlplane.ChecksumOf([]byte("old-beta")),
			}},
		},
		{name: "a stale content that the budget dropped is not listed", cfg: statedir.Config{MaxBytes: 5}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fsys := &memFS{disk: newMemDisk(), faults: &faults{}}
			first := openStore(t, fsys, tt.cfg)
			first.saveAndFlush(withStale(persistOf(etagOne, manifestOne, contentAlpha), "old-beta"))
			first.close()

			restored, _, err := restoreOf(t, fsys)
			require.NoError(t, err)
			assert.Equal(t, tt.wantStale, restored.Stale)
		})
	}
}

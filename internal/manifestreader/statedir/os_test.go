package statedir_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/manifestreader"
	"github.com/blackbirdworks/semaphore/internal/manifestreader/statedir"
)

func TestOS_TornFilesAreIgnored(t *testing.T) {
	t.Parallel()

	tests := []struct {
		damage      func(t *testing.T, dir string)
		name        string
		wantRestore string
	}{
		{
			name: "manifest copy cut in half", wantRestore: manifestreader.RestoreEmpty,
			damage: func(t *testing.T, dir string) {
				t.Helper()

				name := filepath.Join(dir, manifestName)
				info, err := os.Stat(name)
				require.NoError(t, err)
				require.NoError(t, os.Truncate(name, info.Size()/2))
			},
		},
		{
			name: "object cut in half", wantRestore: manifestreader.RestorePartial,
			damage: func(t *testing.T, dir string) {
				t.Helper()

				require.NoError(t, os.Truncate(filepath.Join(dir, "objects", sumOf("alpha-content")), 3))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := filepath.Join(t.TempDir(), "state")
			run := openStore(t, statedir.NewOSFS(), statedir.Config{Dir: dir})
			run.saveAndFlush(persistOf(etagOne, manifestOne, "alpha-content"))
			run.close()

			tt.damage(t, dir)

			store, err := statedir.New(statedir.Config{Dir: dir})
			require.NoError(t, err)

			defer func() { require.NoError(t, store.Close()) }()

			_, _ = store.Hooks().Restore()
			assert.Equal(t, tt.wantRestore, store.Hooks().StateStatus().Restore)
		})
	}
}

// TestOS_ADirectoryWhereAFileBelongsDoesNotBlockTheSaves: a directory under the name of an object or of the manifest
// copy cannot be replaced by a rename. An empty one is removed and replaced; one with content is never removed, the
// object is left out of the save (a partial save) and the manifest copy is reported as unsafe.
func TestOS_ADirectoryWhereAFileBelongsDoesNotBlockTheSaves(t *testing.T) {
	t.Parallel()

	objectOf := func(dir string) string { return filepath.Join(dir, "objects", sumOf(contentBeta)) }
	manifestOf := func(dir string) string { return filepath.Join(dir, manifestName) }

	nothing := func(*testing.T, string) {}
	content := func(t *testing.T, victim string) {
		t.Helper()
		require.NoError(t, os.WriteFile(filepath.Join(victim, keepName), []byte("mine"), 0o600))
	}

	tests := []struct {
		victim         func(dir string) string
		fill           func(t *testing.T, victim string)
		wantRestoreErr error
		name           string
		wantSaveError  string
		wantInVictim   []string
		wantRestored   wantRestored
		wantPartial    bool
	}{
		{
			name:   "an empty directory at an object",
			victim: objectOf,
			fill:   nothing,
			wantRestored: wantRestored{
				etag:     etagOne,
				manifest: manifestOne,
				objects:  []string{contentAlpha, contentBeta},
			},
		},
		{
			name: "a directory with content at an object", victim: objectOf, fill: content,
			wantInVictim: []string{keepName}, wantPartial: true,
			wantRestored: wantRestored{etag: etagOne, manifest: manifestOne, objects: []string{contentAlpha}},
		},
		{
			name:   "an empty directory at the manifest copy",
			victim: manifestOf,
			fill:   nothing,
			wantRestored: wantRestored{
				etag:     etagOne,
				manifest: manifestOne,
				objects:  []string{contentAlpha, contentBeta},
			},
		},
		{
			name: "a directory with content at the manifest copy", victim: manifestOf, fill: content,
			wantInVictim: []string{keepName}, wantSaveError: statedir.ReasonUnsafe,
			wantRestoreErr: manifestreader.ErrNothingRestored,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := filepath.Join(t.TempDir(), "state")
			persist := persistOf(etagOne, manifestOne, contentAlpha, contentBeta)

			first := openStore(t, statedir.NewOSFS(), statedir.Config{Dir: dir})
			first.saveAndFlush(persist)
			first.close()

			victim := tt.victim(dir)

			require.NoError(t, os.Remove(victim))
			require.NoError(t, os.Mkdir(victim, 0o700))
			tt.fill(t, victim)

			second := openStore(t, statedir.NewOSFS(), statedir.Config{Dir: dir})

			_, _ = second.store.Hooks().Restore()
			second.saveAndFlush(persist)

			status := second.store.Hooks().StateStatus()
			assert.Equal(t, tt.wantSaveError, status.LastSaveError)
			assert.Equal(t, tt.wantPartial, status.LastSavePartial)
			second.close()

			assert.Equal(t, tt.wantInVictim, entriesIn(t, victim), "a directory with content is never removed")

			third, err := statedir.New(statedir.Config{Dir: dir})
			require.NoError(t, err)

			defer func() { require.NoError(t, third.Close()) }()

			restored, err := third.Hooks().Restore()
			require.ErrorIs(t, err, tt.wantRestoreErr)
			assert.Equal(t, tt.wantRestored, summaryOf(restored))
		})
	}
}

// entriesIn is the sorted names in a directory, and nil for anything else (a regular file, a missing name).
func entriesIn(tb testing.TB, name string) []string {
	tb.Helper()

	entries, err := os.ReadDir(name)
	if err != nil {
		return nil
	}

	var names []string

	for _, entry := range entries {
		names = append(names, entry.Name())
	}

	return names
}

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

	tests := []struct {
		name          string
		wantSaveError string
		wantObjects   []string
		inManifest    bool
		withContent   bool
		wantPartial   bool
	}{
		{name: "an empty directory at an object", wantObjects: []string{contentAlpha, contentBeta}},
		{
			name: "a directory with content at an object", withContent: true, wantObjects: []string{contentAlpha},
			wantPartial: true,
		},
		{
			name:        "an empty directory at the manifest copy",
			inManifest:  true,
			wantObjects: []string{contentAlpha, contentBeta},
		},
		{
			name: "a directory with content at the manifest copy", inManifest: true, withContent: true,
			wantSaveError: statedir.ReasonUnsafe,
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

			victim := filepath.Join(dir, "objects", sumOf(contentBeta))
			if tt.inManifest {
				victim = filepath.Join(dir, manifestName)
			}

			require.NoError(t, os.Remove(victim))
			require.NoError(t, os.Mkdir(victim, 0o700))

			if tt.withContent {
				require.NoError(t, os.WriteFile(filepath.Join(victim, "keep"), []byte("mine"), 0o600))
			}

			second := openStore(t, statedir.NewOSFS(), statedir.Config{Dir: dir})

			_, _ = second.store.Hooks().Restore()
			second.saveAndFlush(persist)

			status := second.store.Hooks().StateStatus()
			assert.Equal(t, tt.wantSaveError, status.LastSaveError)
			assert.Equal(t, tt.wantPartial, status.LastSavePartial)
			second.close()

			if tt.withContent {
				content, err := os.ReadFile(filepath.Join(victim, "keep"))
				require.NoError(t, err)
				assert.Equal(t, "mine", string(content), "a directory with content is never removed")

				if tt.wantSaveError != "" {
					return
				}
			}

			third, err := statedir.New(statedir.Config{Dir: dir})
			require.NoError(t, err)

			defer func() { require.NoError(t, third.Close()) }()

			restored, err := third.Hooks().Restore()
			require.NoError(t, err)
			assertRestored(t, wantRestored{etag: etagOne, manifest: manifestOne, objects: tt.wantObjects}, restored)
		})
	}
}

package statedir

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errSpy = errors.New("sync failed")

// TestOSFS_OpenFlushesTheParentOfEachDirectoryItCreates: a state directory that Open creates is reachable after a
// power loss only when the entry of every created directory is durable, so the parent of each is flushed, top down;
// an existing directory costs no flush.
func TestOSFS_OpenFlushesTheParentOfEachDirectoryItCreates(t *testing.T) {
	t.Parallel()

	base := t.TempDir()

	tests := []struct {
		syncErr    error
		wantErr    error
		name       string
		dir        string
		wantSynced []string
	}{
		{name: "three new directories", dir: filepath.Join(base, "a", "b", "c"), wantSynced: []string{
			base, filepath.Join(base, "a"), filepath.Join(base, "a", "b"),
		}},
		{name: "one new directory", dir: filepath.Join(base, "one"), wantSynced: []string{base}},
		{name: "an existing directory", dir: base},
		{
			name:       "a flush that fails",
			dir:        filepath.Join(base, "fails"),
			syncErr:    errSpy,
			wantErr:    errSpy,
			wantSynced: []string{base},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var synced []string

			opener := OSFS{syncDir: func(path string) error {
				synced = append(synced, path)

				return tt.syncErr
			}}

			root, err := opener.Open(tt.dir)

			assert.Equal(t, tt.wantSynced, synced)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)

				return
			}

			require.NoError(t, err)
			require.NoError(t, root.Close())

			info, statErr := os.Stat(tt.dir)
			require.NoError(t, statErr)
			assert.True(t, info.IsDir())
		})
	}
}

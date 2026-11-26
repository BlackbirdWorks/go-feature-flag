package statedir

import (
	"io/fs"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOSFS_RefusesADirectoryOfAnotherUser(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantErr error
		owner   func(fs.FileInfo) bool
		name    string
	}{
		{name: "a directory of this user", owner: func(fs.FileInfo) bool { return true }},
		{name: "a directory of another user", owner: func(fs.FileInfo) bool { return false }, wantErr: ErrUnsafe},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root, err := osFS{owner: tt.owner}.Open(t.TempDir())
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)

				return
			}

			require.NoError(t, err)
			require.NoError(t, root.Close())
		})
	}
}

func TestOwnedByProcess(t *testing.T) {
	t.Parallel()

	info, err := osFS{}.Open(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, info.Close())

	stat, err := os.Stat(t.TempDir())
	require.NoError(t, err)
	require.True(t, ownedByProcess(stat), "a directory the test just made is ours")
}

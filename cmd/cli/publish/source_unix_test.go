//go:build unix

package publish_test

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/cli/publish"
)

// loadWithin runs LoadSource and fails the test when it blocks: opening a named pipe would.
func loadWithin(t *testing.T, dir string) error {
	t.Helper()

	done := make(chan error, 1)

	go func() {
		_, err := publish.LoadSource(os.DirFS(dir), srcDir, 0)
		done <- err
	}()

	select {
	case err := <-done:
		return err
	case <-time.After(10 * time.Second):
		require.FailNow(t, "LoadSource blocked: it opened a file that is not regular")

		return nil
	}
}

func TestLoadSourceRefusesRealNonRegularFiles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		make func(t *testing.T, dir string)
		name string
		want string
	}{
		{
			name: "a symbolic link to a file outside the tree",
			make: func(t *testing.T, dir string) {
				t.Helper()

				outside := filepath.Join(t.TempDir(), "secret.yaml")
				require.NoError(t, os.WriteFile(outside, []byte("outside"), 0o600))
				require.NoError(t, os.Symlink(outside, filepath.Join(dir, "flags", "link.yaml")))
			},
			want: `"src/flags/link.yaml"`,
		},
		{
			name: "a symbolic link to a directory outside the tree",
			make: func(t *testing.T, dir string) {
				t.Helper()

				require.NoError(t, os.Symlink(t.TempDir(), filepath.Join(dir, "flagsets")))
			},
			want: `"src/flagsets"`,
		},
		{
			name: "a named pipe",
			make: func(t *testing.T, dir string) {
				t.Helper()
				require.NoError(t, syscall.Mkfifo(filepath.Join(dir, "flags", "pipe.yaml"), 0o600))
			},
			want: `"src/flags/pipe.yaml"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			require.NoError(t, os.MkdirAll(filepath.Join(dir, "flags"), 0o750))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "flags", "ok.yaml"), []byte("ok"), 0o600))
			tt.make(t, dir)

			err := loadWithin(t, dir)
			require.ErrorIs(t, err, publish.ErrNotRegular)
			assert.Contains(t, err.Error(), tt.want)
			assert.NotContains(t, err.Error(), "outside")
		})
	}
}

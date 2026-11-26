//go:build unix

package statedir_test

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/manifestreader"
	"github.com/blackbirdworks/semaphore/internal/manifestreader/statedir"
)

// TestOS_RestoreDiscardsIrregularFilesWithoutBlocking puts a FIFO, a symbolic link, a directory and a sparse file
// of a terabyte where a manifest copy or an object is expected. A FIFO that is opened blocks until a writer comes:
// the restore must return, with the file discarded, and must not read the sparse file.
func TestOS_RestoreDiscardsIrregularFilesWithoutBlocking(t *testing.T) {
	t.Parallel()

	const terabyte = 1 << 40

	tests := []struct {
		replace     func(t *testing.T, name string)
		name        string
		wantRestore string
		onManifest  bool
	}{
		{
			name: "a FIFO", wantRestore: manifestreader.RestorePartial,
			replace: func(t *testing.T, name string) {
				t.Helper()
				require.NoError(t, syscall.Mkfifo(name, 0o600))
			},
		},
		{
			name: "a symbolic link to a file that is fine", wantRestore: manifestreader.RestorePartial,
			replace: func(t *testing.T, name string) {
				t.Helper()

				target := filepath.Join(filepath.Dir(name), "target")
				require.NoError(t, os.WriteFile(target, []byte("x"), 0o600))
				require.NoError(t, os.Symlink(target, name))
			},
		},
		{
			name: "a directory", wantRestore: manifestreader.RestorePartial,
			replace: func(t *testing.T, name string) {
				t.Helper()
				require.NoError(t, os.Mkdir(name, 0o700))
			},
		},
		{
			name: "a sparse file of a terabyte", wantRestore: manifestreader.RestorePartial,
			replace: func(t *testing.T, name string) {
				t.Helper()
				require.NoError(t, os.WriteFile(name, nil, 0o600))
				require.NoError(t, os.Truncate(name, terabyte))
			},
		},
		{
			name: "a FIFO as the manifest copy", onManifest: true, wantRestore: manifestreader.RestoreEmpty,
			replace: func(t *testing.T, name string) {
				t.Helper()
				require.NoError(t, syscall.Mkfifo(name, 0o600))
			},
		},
		{
			name: "a sparse file of a terabyte as the manifest copy", onManifest: true,
			wantRestore: manifestreader.RestoreEmpty,
			replace: func(t *testing.T, name string) {
				t.Helper()
				require.NoError(t, os.WriteFile(name, nil, 0o600))
				require.NoError(t, os.Truncate(name, terabyte))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := filepath.Join(t.TempDir(), "state")
			run := openStore(t, statedir.NewOSFS(), statedir.Config{Dir: dir})
			run.saveAndFlush(persistOf(etagOne, manifestOne, contentAlpha, contentBeta))
			run.close()

			victim := filepath.Join(dir, "objects", sumOf(contentBeta))
			if tt.onManifest {
				victim = filepath.Join(dir, manifestName)
			}

			require.NoError(t, os.Remove(victim))
			tt.replace(t, victim)

			store, err := statedir.New(statedir.Config{Dir: dir})
			require.NoError(t, err)

			// no deferred Close: a restore that blocks on a FIFO holds the store, and the test must fail, not hang
			type result struct {
				restored *manifestreader.Restored
				err      error
			}

			done := make(chan result, 1)

			go func() {
				restored, restoreErr := store.Hooks().Restore()
				done <- result{restored, restoreErr}
			}()

			select {
			case got := <-done:
				require.NoError(t, store.Close())
				assert.Equal(t, tt.wantRestore, store.Hooks().StateStatus().Restore)

				if tt.onManifest {
					require.ErrorIs(t, got.err, manifestreader.ErrNothingRestored)

					return
				}

				require.NoError(t, got.err)
				assertRestored(
					t,
					wantRestored{etag: etagOne, manifest: manifestOne, objects: []string{contentAlpha}},
					got.restored,
				)
			case <-time.After(wait):
				require.FailNow(t, "the restore blocked on an irregular file")
			}
		})
	}
}

//go:build unix

package statedir_test

import (
	"maps"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/manifestreader/statedir"
)

// refusalCause is a directory that is not ours: prepare makes it at path and returns the paths whose whole trees
// must be the same after the refusal.
type refusalCause struct {
	prepare    func(t *testing.T, path string) []string
	name       string
	wantErr    error
	wantReason string
}

func refusalCauses() []refusalCause {
	return []refusalCause{
		{
			name: "foreign files and no VERSION", wantErr: statedir.ErrUnsafe, wantReason: statedir.DisabledUnsafe,
			prepare: func(t *testing.T, path string) []string {
				t.Helper()
				require.NoError(t, os.Mkdir(path, 0o700))
				require.NoError(t, os.WriteFile(filepath.Join(path, "notes.txt"), []byte("keep me"), 0o600))

				return []string{path}
			},
		},
		{
			name: "a foreign .tmp-abc alone", wantErr: statedir.ErrUnsafe, wantReason: statedir.DisabledUnsafe,
			prepare: func(t *testing.T, path string) []string {
				t.Helper()
				require.NoError(t, os.Mkdir(path, 0o700))
				require.NoError(t, os.WriteFile(filepath.Join(path, foreignTmp), []byte("another tool"), 0o600))

				return []string{path}
			},
		},
		{
			name: "a foreign VERSION", wantErr: statedir.ErrForeignVersion, wantReason: statedir.DisabledForeignVersion,
			prepare: func(t *testing.T, path string) []string {
				t.Helper()
				require.NoError(t, os.Mkdir(path, 0o700))
				require.NoError(
					t,
					os.WriteFile(filepath.Join(path, versionName), []byte("semaphore-manifest-state 2\n"), 0o600),
				)
				require.NoError(t, os.WriteFile(filepath.Join(path, manifestName), []byte("future"), 0o600))

				return []string{path}
			},
		},
		{
			name: "VERSION is a directory", wantErr: statedir.ErrUnsafe, wantReason: statedir.DisabledUnsafe,
			prepare: func(t *testing.T, path string) []string {
				t.Helper()
				require.NoError(t, os.MkdirAll(filepath.Join(path, versionName), 0o700))

				return []string{path}
			},
		},
		{
			name: "world-writable", wantErr: statedir.ErrUnsafe, wantReason: statedir.DisabledUnsafe,
			prepare: func(t *testing.T, path string) []string {
				t.Helper()
				require.NoError(t, os.Mkdir(path, 0o700))
				require.NoError(t, os.WriteFile(filepath.Join(path, "notes.txt"), []byte("keep me"), 0o600))
				require.NoError(t, os.Chmod(path, 0o777))

				return []string{path}
			},
		},
		{
			name: "a symbolic link", wantErr: statedir.ErrUnsafe, wantReason: statedir.DisabledUnsafe,
			prepare: func(t *testing.T, path string) []string {
				t.Helper()

				target := path + ".target"
				require.NoError(t, os.Mkdir(target, 0o700))
				require.NoError(t, os.WriteFile(filepath.Join(target, "notes.txt"), []byte("keep me"), 0o600))
				require.NoError(t, os.Symlink(target, path))

				return []string{path, target}
			},
		},
	}
}

func imagesOf(tb testing.TB, paths []string) map[string]string {
	tb.Helper()

	out := map[string]string{}

	for _, path := range paths {
		maps.Copy(out, treeImage(tb, path))
	}

	return out
}

// refusal is what happened when a directory that is not ours was opened: the error, and the trees of the watched
// paths before and after.
type refusal struct {
	err    error
	before map[string]string
	after  map[string]string
}

// refusalMode is a way to open a state directory that is refused.
type refusalMode struct {
	attempt func(t *testing.T, path string, cause refusalCause) refusal
	name    string
}

func refusalModes() []refusalMode {
	return []refusalMode{
		{name: "New", attempt: func(t *testing.T, path string, cause refusalCause) refusal {
			t.Helper()

			watch := cause.prepare(t, path)
			before := imagesOf(t, watch)
			store, err := statedir.New(statedir.Config{Dir: path})

			if store != nil {
				require.NoError(t, store.Close())
			}

			return refusal{err: err, before: before, after: imagesOf(t, watch)}
		}},
		{name: "OpenOrDisable", attempt: func(t *testing.T, path string, cause refusalCause) refusal {
			t.Helper()

			watch := cause.prepare(t, path)
			before := imagesOf(t, watch)
			store, _, reason := statedir.OpenOrDisable(statedir.Config{Dir: path})

			if store != nil {
				require.NoError(t, store.Close())
			}

			assert.Equal(t, cause.wantReason, reason)

			return refusal{err: cause.wantErr, before: before, after: imagesOf(t, watch)}
		}},
		{name: "a live store reopens", attempt: func(t *testing.T, path string, cause refusalCause) refusal {
			t.Helper()

			// a valid store whose first save is rejected, so that the next write opens the path again
			run := openStore(t, statedir.NewOSFS(), statedir.Config{Dir: path})
			run.saveAndFlush(persistOf(etagOne, manifestOne, contentAlpha))
			require.NoError(
				t,
				os.WriteFile(filepath.Join(path, versionName), []byte("semaphore-manifest-state 2\n"), 0o600),
			)
			run.saveAndFlush(persistOf(etagTwo, manifestTwo, contentAlpha))
			require.Equal(t, statedir.ReasonUnsafe, run.store.Hooks().StateStatus().LastSaveError)

			// the path is now the cause, which has no lock file
			require.NoError(t, os.Rename(path, path+".old"))

			watch := cause.prepare(t, path)
			before := imagesOf(t, watch)

			run.saveAndFlush(persistOf(etagTwo, manifestTwo, contentAlpha))

			assert.Equal(t, statedir.ReasonUnsafe, run.store.Hooks().StateStatus().LastSaveError)

			return refusal{err: cause.wantErr, before: before, after: imagesOf(t, watch)}
		}},
	}
}

// TestOS_ARefusedDirectoryIsLeftExactlyAsItWasFound: a directory that is not ours is refused by New, by OpenOrDisable
// and by the reopen of a live store, and in every case the whole tree of the refused directory is the same before
// and after: the lock file that the open creates is removed again, and nothing else is touched.
func TestOS_ARefusedDirectoryIsLeftExactlyAsItWasFound(t *testing.T) {
	t.Parallel()

	for _, cause := range refusalCauses() {
		for _, mode := range refusalModes() {
			t.Run(cause.name+"/"+mode.name, func(t *testing.T) {
				t.Parallel()

				got := mode.attempt(t, filepath.Join(t.TempDir(), "state"), cause)

				require.ErrorIs(t, got.err, cause.wantErr)
				assert.Equal(t, got.before, got.after, "the refused directory is byte-identical")
			})
		}
	}
}

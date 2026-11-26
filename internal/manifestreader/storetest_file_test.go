package manifestreader_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/manifestreader"
	"github.com/blackbirdworks/semaphore/internal/manifestreader/storetest"
)

// The reasons the file store gives for what it does not offer.
const (
	reasonNoIOError  = "an I/O error cannot be arranged on a real directory"
	reasonNoThrottle = "a file system does not throttle"
	reasonNoHang     = "a local read cannot be held: a FIFO is refused, and the context is supplied by the suite " +
		"(the cancel and deadline paths of the reads are unit-tested in filestore_internal_test.go)"
	reasonNoVersions   = "a file has no versions"
	reasonNoNested     = "a path cannot be a file and a directory"
	reasonWindowsNames = "Windows file names cannot contain a question mark or a colon"
)

var errNoSuchFault = errors.New("a real directory cannot arrange this")

// fileBackend arranges files in a real directory. A denied key is a symbolic link that leaves the root, which is
// refused whoever the process runs as.
type fileBackend struct {
	root    string
	outside string
	seq     atomic.Int64
}

var _ storetest.Backend = (*fileBackend)(nil)

// Put writes a temporary file and renames it over the key: a reader sees the old or the new content.
func (b *fileBackend) Put(key string, content []byte) error {
	target := b.path(key)

	err := os.MkdirAll(filepath.Dir(target), 0o750)
	if err != nil {
		return err
	}

	tmp := filepath.Join(b.root, ".tmp-"+strconv.FormatInt(b.seq.Add(1), 10))

	err = os.WriteFile(tmp, content, 0o600)
	if err != nil {
		return err
	}

	return os.Rename(tmp, target)
}

// Overwrite rewrites the file where it is (same inode) and puts the modification time back, so that a rewrite of
// the same length leaves the size, the time and the inode as they were.
func (b *fileBackend) Overwrite(key string, content []byte) error {
	target := b.path(key)

	info, err := os.Stat(target)
	if err != nil {
		return b.Put(key, content)
	}

	err = os.WriteFile(target, content, 0o600)
	if err != nil {
		return err
	}

	return os.Chtimes(target, info.ModTime(), info.ModTime())
}

func (b *fileBackend) Delete(key string) error {
	return os.Remove(b.path(key))
}

// Fail denies a key by putting a link that leaves the root where the file is, and keeps the file next to it.
func (b *fileBackend) Fail(key string, fault storetest.Fault) error {
	target := b.path(key)
	held := target + ".held"

	switch fault {
	case storetest.FaultNone:
		_, err := os.Lstat(held)
		if errors.Is(err, os.ErrNotExist) {
			return nil // nothing was denied
		}

		err = os.Remove(target)
		if err != nil {
			return err
		}

		return os.Rename(held, target)
	case storetest.FaultDenied:
		err := os.Rename(target, held)
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(filepath.Dir(target), b.outside)
		if err != nil {
			return err
		}

		return os.Symlink(rel, target)
	case storetest.FaultTransient, storetest.FaultThrottled:
	}

	return errNoSuchFault
}

func (*fileBackend) Hang(string) (*storetest.Hang, error)     { return nil, storetest.ErrUnsupported }
func (*fileBackend) HangBody(string) (*storetest.Hang, error) { return nil, storetest.ErrUnsupported }

func (*fileBackend) PutVersion(string, []byte) (string, error) { return "", storetest.ErrUnsupported }

func (b *fileBackend) path(key string) string { return filepath.Join(b.root, filepath.FromSlash(key)) }

// fileWaivers is what the file store does not offer.
func fileWaivers() storetest.Waivers {
	waivers := storetest.NewWaivers().
		With(storetest.CapTransient, reasonNoIOError).
		With(storetest.CapThrottle, reasonNoThrottle).
		With(storetest.CapHang, reasonNoHang).
		With(storetest.CapHangBody, reasonNoHang).
		With(storetest.CapVersions, reasonNoVersions).
		With(storetest.CapNestedKeys, reasonNoNested)

	if runtime.GOOS == "windows" {
		waivers.With(storetest.CapReservedNameChars, reasonWindowsNames)
	}

	return waivers
}

func fileFixture(t *testing.T) storetest.Fixture {
	t.Helper()

	base := t.TempDir()
	root := filepath.Join(base, "root")
	require.NoError(t, os.Mkdir(root, 0o750))

	outside := filepath.Join(base, "outside.txt")
	require.NoError(t, os.WriteFile(outside, []byte("outside the root"), 0o600))

	store, err := manifestreader.NewFileStore(root)
	require.NoError(t, err)

	return storetest.Fixture{
		Store: store, Backend: &fileBackend{root: root, outside: outside}, Unsupported: fileWaivers(),
	}
}

func TestConformance_FileStore(t *testing.T) {
	t.Parallel()

	report := storetest.Run(t, "file", fileWaivers(), fileFixture)

	wantUnsupported := map[string]string{
		"transient-is-retryable":               "transient: " + reasonNoIOError,
		"throttled-is-retryable-and-throttled": "throttle: " + reasonNoThrottle,
		"cancel-while-reading":                 "hang: " + reasonNoHang,
		"deadline-while-reading":               "hang: " + reasonNoHang,
		"cancel-leaves-nothing-behind":         "hang: " + reasonNoHang,
		caseBodyCancelName:                     "hang-body: " + reasonNoHang,
		caseVersionsName:                       "versions: " + reasonNoVersions,
		"object-and-child-keys-coexist":        "nested-keys: " + reasonNoNested,
	}

	if runtime.GOOS == "windows" {
		wantUnsupported["key-characters-reserved-by-some-file-systems"] = "reserved-name-chars: " + reasonWindowsNames
	}

	assert.Equal(t, wantUnsupported, report.Unsupported)
	assert.ElementsMatch(t, ranCases(report.Unsupported), report.Ran)
	assert.Contains(t, report.Ran, "denied-is-not-retryable", "denied runs whoever the process is")
	assert.Contains(t, report.Ran, caseKeyRefusalName)
	assert.Contains(t, report.Ran, "missing-version-is-not-found")
}

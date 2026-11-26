package manifestreader_test

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/manifestreader"
	"github.com/blackbirdworks/semaphore/internal/manifestreader/storetest"
)

var errNoSuchFault = errors.New("a real directory cannot arrange this fault")

// The reasons the file backend gives for the cases it cannot run.
const (
	reasonNoIOError  = "an I/O error cannot be arranged on a real directory"
	reasonNoThrottle = "a file system does not throttle"
	reasonNoHang     = "a local read cannot be held without a special file, which the store refuses"
	reasonNoDeny     = "this process reads files whatever their mode (root, or no permission bits)"
)

// fileBackend arranges files in a real directory.
type fileBackend struct {
	root string
	seq  atomic.Int64
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

func (b *fileBackend) Fail(key string, fault storetest.Fault) error {
	switch fault {
	case storetest.FaultNone:
		return os.Chmod(b.path(key), 0o600)
	case storetest.FaultDenied:
		return os.Chmod(b.path(key), 0)
	case storetest.FaultTransient, storetest.FaultThrottled:
	}

	return errNoSuchFault
}

func (*fileBackend) Hang(string) (*storetest.Hang, error) { return nil, errNoSuchFault }

func (b *fileBackend) path(key string) string { return filepath.Join(b.root, filepath.FromSlash(key)) }

// canReadDespitePermissions reports whether this process reads a file whose mode is 0: root, or a file system
// without permission bits.
func canReadDespitePermissions(dir string) bool {
	probe := filepath.Join(dir, "probe")
	if os.WriteFile(probe, []byte("x"), 0o600) != nil || os.Chmod(probe, 0) != nil {
		return true
	}

	file, err := os.Open(probe)
	if err != nil {
		return false
	}

	_ = file.Close()

	return true
}

func fileFixture(t *testing.T) storetest.Fixture {
	t.Helper()

	dir := t.TempDir()
	store, err := manifestreader.NewFileStore(dir)
	require.NoError(t, err)

	unsupported := make(map[storetest.Capability]string)
	unsupported[storetest.CapTransient] = reasonNoIOError
	unsupported[storetest.CapThrottle] = reasonNoThrottle
	unsupported[storetest.CapHang] = reasonNoHang

	if canReadDespitePermissions(t.TempDir()) {
		unsupported[storetest.CapDeny] = reasonNoDeny
	}

	return storetest.Fixture{Store: store, Backend: &fileBackend{root: dir}, Unsupported: unsupported}
}

func TestConformance_FileStore(t *testing.T) {
	t.Parallel()

	report := storetest.Run(t, "file", fileFixture)

	wantUnsupported := map[string]string{
		"transient-is-retryable":               "transient: " + reasonNoIOError,
		"throttled-is-retryable-and-throttled": "throttle: " + reasonNoThrottle,
		"cancel-while-reading":                 "hang: " + reasonNoHang,
		"deadline-while-reading":               "hang: " + reasonNoHang,
		"cancel-leaves-nothing-behind":         "hang: " + reasonNoHang,
	}

	if canReadDespitePermissions(t.TempDir()) {
		wantUnsupported["denied-is-not-retryable"] = "deny: " + reasonNoDeny
	}

	assert.Equal(t, wantUnsupported, report.Unsupported)
	assert.ElementsMatch(t, ranCases(report.Unsupported), report.Ran)
	assert.Contains(t, report.Ran, "unclean-keys-are-refused")
	assert.Contains(t, report.Ran, "missing-version-is-not-found")
}

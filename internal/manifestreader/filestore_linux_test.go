//go:build linux

package manifestreader_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/manifestreader"
)

// procFD lists the descriptors of the process.
const procFD = "/proc/self/fd"

// openFilesUnder counts the descriptors of this process that point into dir. Counting only those makes the check
// independent of what the tests running in parallel open.
func openFilesUnder(t *testing.T, dir string) int {
	t.Helper()

	entries, err := os.ReadDir(procFD)
	require.NoError(t, err)

	count := 0

	for _, entry := range entries {
		target, linkErr := os.Readlink(procFD + "/" + entry.Name())
		if linkErr == nil && strings.HasPrefix(target, dir+string(filepath.Separator)) {
			count++
		}
	}

	return count
}

// TestFileStore_LeavesNoDescriptorOpen: every outcome of a Get, the failing ones included, closes the file.
func TestFileStore_LeavesNoDescriptorOpen(t *testing.T) {
	t.Parallel()

	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)

	writeTree(t, dir, map[string]string{fileA: "some content", "empty.txt": "", "sub/b.txt": "b"})
	require.NoError(t, os.Chmod(filepath.Join(dir, fileA), 0o600))

	store := newFileStoreAt(t, dir)
	etag := mustGetFile(t, store, fileA, "").ETag

	canceled, cancel := context.WithCancel(t.Context())
	cancel()

	requests := []manifestreader.GetRequest{
		{Key: fileA, Limit: 100},                // a body
		{Key: fileA, ETag: etag, Limit: 100},    // not modified
		{Key: fileA, ETag: `"old"`, Limit: 100}, // changed
		{Key: fileA, Limit: 3},                  // too large
		{Key: "missing.txt", Limit: 100},        // missing
		{Key: "sub", Limit: 100},                // a directory
		{Key: "../a.txt", Limit: 100},           // a refused key
		{Key: "empty.txt", Limit: 0},            // an empty file
	}

	for range 25 {
		for _, req := range requests {
			_, _ = store.Get(t.Context(), req)
		}

		_, _ = store.Get(canceled, manifestreader.GetRequest{Key: fileA, Limit: 100})
	}

	assert.Equal(t, 0, openFilesUnder(t, dir), "no descriptor on a file of the root is left open")
}

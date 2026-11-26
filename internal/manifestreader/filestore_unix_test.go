//go:build unix

package manifestreader_test

import (
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/manifestreader"
)

// getBounded runs Get and fails the test when it does not return: a refused file must never block.
func getBounded(t *testing.T, store *manifestreader.FileStore, key string, unblock func()) error {
	t.Helper()

	done := make(chan error, 1)

	go func() {
		_, err := store.Get(t.Context(), manifestreader.GetRequest{Key: key, Limit: 10})
		done <- err
	}()

	select {
	case err := <-done:
		return err
	case <-time.After(wait):
		unblock()

		require.FailNow(t, "Get blocked on an irregular file")

		return nil
	}
}

func TestFileStore_FIFOIsRefusedWithoutBlocking(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	fifo := filepath.Join(dir, "pipe")
	require.NoError(t, syscall.Mkfifo(fifo, 0o600))

	err := getBounded(t, newFileStoreAt(t, dir), "pipe", func() {
		// a writer ends the wait of a blocked open
		file, openErr := os.OpenFile(fifo, os.O_RDWR, 0)
		if openErr == nil {
			_ = file.Close()
		}
	})

	wantDenied(t, err, manifestreader.ErrNotRegular)
}

func TestFileStore_SocketIsRefused(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "sock")

	if len(path) > 100 {
		t.Skipf("the temporary directory path is too long for a unix socket (%d bytes)", len(path))
	}

	var lc net.ListenConfig

	listener, err := lc.Listen(t.Context(), "unix", path)
	require.NoError(t, err)

	defer func() { _ = listener.Close() }()

	err = getBounded(t, newFileStoreAt(t, dir), "sock", func() {})
	wantDenied(t, err, syscall.ENXIO)
}

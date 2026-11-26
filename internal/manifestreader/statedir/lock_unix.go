//go:build unix

package statedir

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// lockFileExclusive takes a non-blocking exclusive flock; the kernel drops it when the process ends, so a crash
// leaves no stale lock.
func lockFileExclusive(file *os.File) error {
	err := syscall.Flock(
		int(file.Fd()),
		syscall.LOCK_EX|syscall.LOCK_NB,
	)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return ErrLocked
	}

	if err != nil {
		return fmt.Errorf("statedir: lock: %w", err)
	}

	return nil
}

// syncPath flushes the entries of a directory to disk.
func syncPath(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}

	err = dir.Sync()
	_ = dir.Close()

	if err != nil {
		return fmt.Errorf("sync: %w", err)
	}

	return nil
}

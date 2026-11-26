//go:build unix

package statedir

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// hasFileLock tells that this system has a file lock: a directory with one has one writer at a time.
const hasFileLock = true

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

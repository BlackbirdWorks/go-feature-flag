//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package filepublish

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// lock takes the exclusive advisory lock of the store and returns the function that releases it. It blocks until
// the lock is free: the sections it protects are short. The lock is tied to the open file, so the system releases
// it when the process dies.
func lock(root *os.Root) (func(), error) {
	file, err := root.OpenFile(lockName, os.O_RDWR|os.O_CREATE, lockMode)
	if err != nil {
		return nil, fmt.Errorf("lock file: %w", err)
	}

	for {
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX)
		if !errors.Is(err, syscall.EINTR) {
			break
		}
	}

	if err != nil {
		_ = file.Close()

		return nil, fmt.Errorf("lock %s: %w", lockName, err)
	}

	return func() { _ = file.Close() }, nil
}

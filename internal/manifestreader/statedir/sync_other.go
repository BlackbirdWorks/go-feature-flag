//go:build !windows

package statedir

import (
	"fmt"
	"os"
)

// flushDir flushes the entries of an open directory to disk. It is the one place where the platform decides: see
// sync_windows.go.
func flushDir(dir *os.File) error {
	err := dir.Sync()
	if err != nil {
		return fmt.Errorf("sync: %w", err)
	}

	return nil
}

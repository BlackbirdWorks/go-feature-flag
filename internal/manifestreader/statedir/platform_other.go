//go:build !windows

package statedir

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

// This file and platform_windows.go hold everything that the system decides: every rule that is expressed in POSIX
// terms (permission bits, errno values, the flush of a directory) lives here, with its Windows variant next to it.
// The rules that every system can express (a symbolic link or a reparse point is refused, only a regular file is
// read, a name cannot leave the directory) stay in the files that apply them.

// flushDir flushes the entries of an open directory to disk.
func flushDir(dir *os.File) error {
	err := dir.Sync()
	if err != nil {
		return fmt.Errorf("sync: %w", err)
	}

	return nil
}

// worldWritable reports whether anybody can write to the directory: such a directory is refused.
func worldWritable(info fs.FileInfo) bool { return info.Mode().Perm()&0o002 != 0 }

// isNoSpace reports a full disk or an exhausted quota.
func isNoSpace(err error) bool { return errors.Is(err, syscall.ENOSPC) }

// isReadOnly reports a read-only file system.
func isReadOnly(err error) bool { return errors.Is(err, syscall.EROFS) }

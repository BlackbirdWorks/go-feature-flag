//go:build windows

package statedir

import (
	"errors"
	"io/fs"
	"os"
	"syscall"
)

// The values of the errors of Windows that the writer tells apart (the numbers of winerror.h).
const (
	errorWriteProtect   syscall.Errno = 19
	errorDiskFull       syscall.Errno = 112
	errorHandleDiskFull syscall.Errno = 39
)

// flushDir does nothing on Windows: a directory handle opened for reading cannot be flushed there
// (FlushFileBuffers needs write access, and a directory cannot be opened for writing), and the entries of a
// directory are not flushed by the file system the way fsync does on Linux. The state directory is a cache; the
// layout and the manifest are still written to temporary files, flushed, and renamed over their names, but there is
// no guarantee that the directory entries survive a power loss. A restart then reads what is there, verifies it,
// and fetches what is missing.
func flushDir(*os.File) error { return nil }

// worldWritable is always false on Windows: Go reports the mode bits of a directory there from its attributes
// (writable ones are 0777), not from its access control list, so the rule cannot be expressed and is skipped.
func worldWritable(fs.FileInfo) bool { return false }

// isNoSpace reports a full disk (ERROR_DISK_FULL, ERROR_HANDLE_DISK_FULL) or the errno that Go maps.
func isNoSpace(err error) bool {
	return errors.Is(err, syscall.ENOSPC) || errors.Is(err, errorDiskFull) || errors.Is(err, errorHandleDiskFull)
}

// isReadOnly reports a write-protected medium (ERROR_WRITE_PROTECT) or the errno that Go maps.
func isReadOnly(err error) bool {
	return errors.Is(err, syscall.EROFS) || errors.Is(err, errorWriteProtect)
}

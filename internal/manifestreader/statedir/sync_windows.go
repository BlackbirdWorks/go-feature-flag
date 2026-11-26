//go:build windows

package statedir

import "os"

// flushDir does nothing on Windows: a directory handle opened for reading cannot be flushed there
// (FlushFileBuffers needs write access, and a directory cannot be opened for writing), and the entries of a
// directory are not flushed by the file system the way fsync does on Linux. The state directory is a cache; the
// layout and the manifest are still written to temporary files, flushed, and renamed over their names, but there is
// no guarantee that the directory entries survive a power loss. A restart then reads what is there, verifies it,
// and fetches what is missing.
func flushDir(*os.File) error { return nil }

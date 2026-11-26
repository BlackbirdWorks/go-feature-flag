//go:build !unix

package statedir

import (
	"os"
)

// hasFileLock is false here: there is no lock (golang.org/x/sys is not a direct dependency, and the lock is not worth one).
const hasFileLock = false

// lockFileExclusive is a no-op where there is no flock: the directory is then last-writer-wins. Two writers can
// delete each other's objects, and the restore is then partial, never corrupt (see the package documentation).
func lockFileExclusive(*os.File) error { return nil }

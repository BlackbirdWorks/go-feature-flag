//go:build !unix

package statedir

import (
	"os"
)

// lockFileExclusive is a no-op where there is no flock: the directory is then last-writer-wins. Two writers can
// delete each other's objects, and the restore is then partial, never corrupt (see the package documentation).
func lockFileExclusive(*os.File) error { return nil }

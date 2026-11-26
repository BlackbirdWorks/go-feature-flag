//go:build !unix

package statedir

import (
	"os"
)

// lockFileExclusive is a no-op where there is no flock: the directory is then last-writer-wins, which cannot
// corrupt it (see the package documentation).
func lockFileExclusive(*os.File) error { return nil }

// syncPath is a no-op where a directory cannot be flushed.
func syncPath(string) error { return nil }

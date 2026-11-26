//go:build !unix

package statedir

import (
	"io/fs"
	"os"
)

// lockFileExclusive is a no-op where there is no flock: the directory is then last-writer-wins, which cannot
// corrupt it (see the package documentation).
func lockFileExclusive(*os.File) error { return nil }

func ownedByProcess(fs.FileInfo) bool { return true }

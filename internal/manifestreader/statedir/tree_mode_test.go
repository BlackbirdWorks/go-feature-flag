//go:build !windows

package statedir_test

import "io/fs"

// modeNote is how a tree image records the permissions of an entry: on systems that have permission bits the image
// compares them, so a save that changes a file's mode without changing its bytes is seen.
func modeNote(mode fs.FileMode) string {
	return "[" + (mode & (fs.ModePerm | fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky)).String() + "]"
}

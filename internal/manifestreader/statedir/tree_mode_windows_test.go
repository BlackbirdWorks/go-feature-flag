//go:build windows

package statedir_test

import "io/fs"

// modeNote is empty on Windows: the mode bits of a Windows file come from its attributes, not from permissions, so
// a tree image does not compare them.
func modeNote(fs.FileMode) string { return "" }

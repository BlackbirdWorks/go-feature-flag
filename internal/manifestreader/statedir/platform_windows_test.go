//go:build windows

package statedir

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestFlushDir_DoesNothingOnWindows: a directory handle cannot be flushed there, and that must not fail a save.
// (Compiled by the cross build, not run, outside Windows.)
func TestFlushDir_DoesNothingOnWindows(t *testing.T) {
	t.Parallel()

	dir, err := os.Open(t.TempDir())
	require.NoError(t, err)

	defer func() { _ = dir.Close() }()

	require.NoError(t, flushDir(dir))
}

// TestWindowsRules: the rules that Windows cannot express are skipped, and the errors of Windows are mapped.
// (Compiled by the cross build, not run, outside Windows.)
func TestWindowsRules(t *testing.T) {
	t.Parallel()

	info, err := os.Stat(t.TempDir())
	require.NoError(t, err)

	require.False(t, worldWritable(info), "the mode bits of a Windows directory say nothing about its access list")
	require.True(t, isNoSpace(errorDiskFull))
	require.True(t, isNoSpace(errorHandleDiskFull))
	require.True(t, isReadOnly(errorWriteProtect))
	require.False(t, isNoSpace(errorWriteProtect))
}

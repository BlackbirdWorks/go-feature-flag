//go:build windows

package statedir

import (
	"os"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
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

	tests := []struct {
		err          error
		name         string
		wantNoSpace  bool
		wantReadOnly bool
	}{
		{name: "the disk is full", err: errorDiskFull, wantNoSpace: true},
		{name: "the handle disk is full", err: errorHandleDiskFull, wantNoSpace: true},
		{name: "the errno that Go maps", err: syscall.ENOSPC, wantNoSpace: true},
		{name: "the medium is write-protected", err: errorWriteProtect, wantReadOnly: true},
		{name: "the errno of a read-only file system", err: syscall.EROFS, wantReadOnly: true},
		{name: "another error", err: syscall.EIO},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.wantNoSpace, isNoSpace(tt.err))
			assert.Equal(t, tt.wantReadOnly, isReadOnly(tt.err))
		})
	}
}

// TestWorldWritable_IsSkippedOnWindows: the mode bits of a Windows directory say nothing about its access list.
func TestWorldWritable_IsSkippedOnWindows(t *testing.T) {
	t.Parallel()

	info, err := os.Stat(t.TempDir())
	require.NoError(t, err)

	assert.False(t, worldWritable(info))
}

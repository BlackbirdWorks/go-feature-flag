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

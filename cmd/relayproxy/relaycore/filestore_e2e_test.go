package relaycore_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// The scenario on a directory: it always runs, no emulator is needed.
func TestE2E_FileStore(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "control")
	require.NoError(t, os.MkdirAll(dir, 0o750))

	runScenario(t, &storeFixture{
		put: filePut(t, dir),
		// the directory goes away, as an unmounted volume does
		down: func() { require.NoError(t, os.Rename(dir, dir+".gone")) },
		doc:  fileDoc(dir, filepath.Join(t.TempDir(), "state")),
		env:  map[string]string{},
	}, nil)
}

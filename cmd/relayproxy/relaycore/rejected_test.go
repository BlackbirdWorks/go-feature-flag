package relaycore_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/relaycore"
	"github.com/blackbirdworks/semaphore/controlplane"
)

// TestCore_Status: a rejected flag is counted once, in the applied generation whose content brought it, as kept or
// dropped; a later generation that leaves it as it was counts nothing.
func TestCore_Status(t *testing.T) {
	t.Parallel()

	const (
		goodAndBad = "good:\n  variations: {a: 1}\n  defaultRule: {variation: a}\nbad:\n  defaultRule: {variation: a}\n"
		bothBad    = "good:\n  defaultRule: {variation: a}\nbad:\n  defaultRule: {variation: a}\n"
	)

	tests := []struct {
		name        string
		versions    []string
		wantKept    uint64
		wantDropped uint64
	}{
		{name: "valid", versions: []string{string(flagsYAML(1))}},
		{name: "dropped", versions: []string{goodAndBad}, wantDropped: 1},
		{name: "same content again", versions: []string{goodAndBad, goodAndBad, goodAndBad}, wantDropped: 1},
		{name: "kept and dropped", versions: []string{goodAndBad, bothBad}, wantKept: 1, wantDropped: 2},
		{name: "fixed then broken", versions: []string{goodAndBad, string(flagsYAML(1)), goodAndBad}, wantDropped: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := filepath.Join(t.TempDir(), "control")
			require.NoError(t, os.MkdirAll(dir, 0o750))

			deps, harness := newDeps(t, secretOptions(map[string]string{hookEnv: deadURL}))

			pub := &publication{
				t: t, put: filePut(t, dir), flags: map[string][]byte{},
				sets: []controlplane.FlagSet{{Name: "one", Sources: []string{objA}, APIKeys: []string{"key-one"}}},
			}

			harness.start(t, parse(t, fileDoc(dir, ""), secretOptions(map[string]string{hookEnv: deadURL})), deps)
			harness.settle(t)

			for _, version := range tt.versions {
				pub.flags[objA] = []byte(version)
				pub.publish()
				harness.poll(t)
				harness.awaitGeneration(t, pub.gen)
			}

			got := harness.core.Status().Reloads.FlagsRejected

			assert.Equal(t, tt.wantKept, got[relaycore.RejectedKept])
			assert.Equal(t, tt.wantDropped, got[relaycore.RejectedDropped])
		})
	}
}

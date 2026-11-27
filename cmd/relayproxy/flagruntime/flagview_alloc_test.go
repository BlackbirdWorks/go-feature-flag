package flagruntime_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/internal/loadtest"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
)

// The flag change answer and the configuration view: the first Change encodes the flags, every later call is free;
// the configuration view allocates its map and nothing per flag.
//
//nolint:paralleltest // AllocsPerRun counts allocations from concurrent tests across the process
func TestFlagSet_ViewAllocations(t *testing.T) {
	set := runtimeOf(t, []byte(loadtest.ManyFlagsYAML(100)), "yaml")

	first, err := set.Change()
	require.NoError(t, err)

	var memoized flagruntime.FlagChange

	again := testing.AllocsPerRun(100, func() { memoized, _ = set.Change() })
	assert.Zero(t, again, "a memoized Change does not allocate")
	assert.Equal(t, first, memoized)

	second, err := set.Change()
	require.NoError(t, err)
	assert.Equal(t, first, second)

	var listed map[string]flagdef.Flag

	config := testing.AllocsPerRun(100, func() { listed = set.Configuration(nil) })

	assert.Len(t, listed, len(set.Flags))
	assert.Less(t, config, float64(len(set.Flags)), "no copy per flag")
	t.Logf("Change (memoized) %v allocs, Configuration of 100 flags %v allocs", again, config)
}

package flagruntime_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/loadtest"
)

// The flag change answer and the configuration view: the first Change encodes the flags, every later call is free;
// the configuration view allocates its map and nothing per flag.
//
//nolint:paralleltest // AllocsPerRun counts allocations from concurrent tests across the process
func TestFlagSet_ViewAllocations(t *testing.T) {
	set := runtimeOf(t, []byte(loadtest.ManyFlagsYAML(100)), "yaml")

	first, err := set.Change()
	require.NoError(t, err)

	again := testing.AllocsPerRun(100, func() { _, _ = set.Change() })
	assert.Zero(t, again, "a memoized Change does not allocate")

	second, err := set.Change()
	require.NoError(t, err)
	assert.Equal(t, first, second)

	config := testing.AllocsPerRun(100, func() { _ = set.Configuration(nil) })
	assert.Less(t, config, float64(len(set.Flags)), "no copy per flag")
	t.Logf("Change (memoized) %v allocs, Configuration of 100 flags %v allocs", again, config)
}

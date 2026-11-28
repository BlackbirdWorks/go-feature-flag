package flagruntime

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// With no flag set without keys the public view costs nothing: no sorting, no allocation, however many flag sets
// there are.
//
//nolint:paralleltest // testing.AllocsPerRun panics in a parallel test
func TestBuildPublicCostsNothingWithoutKeylessFlagSets(t *testing.T) {
	const sets = 5000

	next := &state{sets: make(map[string]*setState, sets)}

	for i := range sets {
		name := fmt.Sprintf("set-%d", i)
		next.sets[name] = &setState{
			spec: &setSpec{name: name, declKeys: []string{"key-" + name}}, fs: &FlagSet{Name: name},
		}
	}

	stg := &stage{prev: &state{}, now: time.Time{}}

	var buildErr error

	allocations := testing.AllocsPerRun(20, func() { buildErr = stg.buildPublic(next) })

	require.NoError(t, buildErr)
	assert.Nil(t, next.public)
	assert.Zero(t, allocations, "allocations of a build with %d flag sets with keys and none public", sets)
}

package controlsource_test

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/controlplane"
)

const (
	scaleFlagsPerSet   = 100
	scaleSetsPerObject = 10
)

// scaleListing is a fleet of flag sets of 100 flags; ten flag sets share one flags object.
func scaleListing() *listing {
	names := make([]string, scaleFlagsPerSet)
	for i := range names {
		names[i] = fmt.Sprintf("flag-%03d", i)
	}

	plan := &listing{flags: map[string][]byte{}, gen: 1}

	for obj := range scaleSets / scaleSetsPerObject {
		name := fmt.Sprintf("obj-%04d", obj)
		plan.flags[name] = flagsYAML(obj+1, names...)

		for slot := range scaleSetsPerObject {
			set := fmt.Sprintf("set-%04d-%d", obj, slot)
			plan.sets = append(plan.sets, controlplane.FlagSet{
				Name: set, Sources: []string{name}, APIKeys: []string{"key-" + set},
			})
		}
	}

	return plan
}

// TestScaleColdStart: a cold start of the whole fleet costs one fetch per object when the relay has no state
// directory, and no object fetch at all (one conditional poll of the manifest) when it has the state directory of
// an earlier run.
func TestScaleColdStart(t *testing.T) {
	t.Parallel()

	// wantFetched is the list of keys the store sees from the cold start: the manifest and every object, or only the
	// manifest.
	tests := []struct {
		wantFetched func(keys map[string]string) []string
		name        string
		seed        bool
	}{
		{
			name: "without the state directory",
			wantFetched: func(keys map[string]string) []string {
				return append(slices.Collect(maps.Values(keys)), restartManifest)
			},
		},
		{
			name:        "with the state directory",
			seed:        true,
			wantFetched: func(map[string]string) []string { return []string{restartManifest} },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newMemStore()
			dir := filepath.Join(t.TempDir(), "state")
			keys := scaleListing().publish(t, store)

			if tt.seed {
				seed := startProcess(t, store, dir)
				seed.settle()
				require.Equal(t, flagruntime.OutcomeApplied, seed.nextRound().Outcome)
				seed.flush()
				seed.stop()
			}

			store.take()

			proc := startProcess(t, store, dir)
			proc.settle()
			assert.Equal(t, flagruntime.OutcomeApplied, proc.nextRound().Outcome)

			calls := store.take()
			fetched := make([]string, 0, len(calls))

			for _, call := range calls {
				fetched = append(fetched, call.Key)
			}

			assert.Equal(t, sortedKeys(tt.wantFetched(keys)), sortedKeys(fetched))

			run := proc.holder.Load()
			assert.Len(t, run.FlagSets, scaleSets)
			assert.Equal(t, uint64(1), run.Generation)
		})
	}
}

func sortedKeys(in []string) []string { return slices.Sorted(slices.Values(in)) }

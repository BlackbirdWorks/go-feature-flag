package controlsource_test

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/internal/manifestreader"
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

	// wantFetched is the list of requests the store sees from the cold start, with the ETag of each: the manifest
	// and every object (no ETag), or only the conditional poll of the manifest (the stored ETag, answered without a
	// body).
	tests := []struct {
		wantFetched func(keys map[string]string, manifestETag string) []storeCall
		name        string
		wantPoll    string
		seed        bool
	}{
		{
			name:     "without the state directory",
			wantPoll: manifestreader.PollUpdated,
			wantFetched: func(keys map[string]string, _ string) []storeCall {
				calls := make([]storeCall, 0, len(keys)+1)
				calls = append(calls, storeCall{Key: restartManifest})

				for _, key := range keys {
					calls = append(calls, storeCall{Key: key})
				}

				return calls
			},
		},
		{
			name:     "with the state directory",
			seed:     true,
			wantPoll: manifestreader.PollNotModified,
			wantFetched: func(_ map[string]string, manifestETag string) []storeCall {
				return []storeCall{{Key: restartManifest, ETag: manifestETag}}
			},
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

			assert.Equal(t, sortedCalls(tt.wantFetched(keys, store.etag(restartManifest))), sortedCalls(store.take()))
			assert.Equal(t, tt.wantPoll, proc.source.Status().Reader.Poll.Result)

			run := proc.holder.Load()
			assert.Len(t, run.FlagSets, scaleSets)
			assert.Equal(t, uint64(1), run.Generation)
		})
	}
}

func sortedCalls(in []storeCall) []storeCall {
	return slices.SortedFunc(slices.Values(in), func(a, b storeCall) int { return strings.Compare(a.Key, b.Key) })
}

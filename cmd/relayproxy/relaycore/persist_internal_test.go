package relaycore

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/internal/manifestreader"
)

const (
	objFlags = "flags-a"
	objSets  = "sets"
)

func objectOf(kind, content string) manifestreader.ObjectState {
	return manifestreader.ObjectState{
		Kind: kind, Format: "yaml", State: manifestreader.StateOK, Bytes: []byte(content),
		Checksum: controlplane.Checksum{Algorithm: "sha256", Value: kind + "-" + content},
	}
}

func snapshotOf(revision, generation uint64, flags, sets string) manifestreader.Persist {
	return manifestreader.Persist{
		ETag: "etag", Manifest: []byte("manifest"),
		Snapshot: &manifestreader.Snapshot{
			Revision: revision, Generation: generation,
			Objects: map[string]manifestreader.ObjectState{
				objFlags: objectOf(controlplane.KindFlags, flags), objSets: objectOf(controlplane.KindFlagSets, sets),
			},
		},
	}
}

func round(revision uint64, outcome flagruntime.Outcome) *flagruntime.RoundResult {
	return &flagruntime.RoundResult{
		Outcome: outcome, Code: string(outcome),
		Revision: revision,
	}
}

func statuses(invalid ...string) map[string]*flagruntime.ObjectStatus {
	out := map[string]*flagruntime.ObjectStatus{
		objFlags: {Name: objFlags, State: flagruntime.ObjectOK},
		objSets:  {Name: objSets, State: flagruntime.ObjectOK},
	}

	for _, name := range invalid {
		out[name] = &flagruntime.ObjectStatus{Name: name, State: flagruntime.ObjectInvalid}
	}

	return out
}

// step is one thing that happens to the persister: the reader offers a snapshot, or the engine decides a revision.
type step struct {
	offer   *manifestreader.Persist
	result  *flagruntime.RoundResult
	objects map[string]*flagruntime.ObjectStatus
}

// The state directory is handed what the engine applied and nothing else, with the content that the runtime serves.
func TestPersisterHandsOverWhatTheEngineApplied(t *testing.T) {
	t.Parallel()

	one, two, three := snapshotOf(1, 1, "v1", "s1"), snapshotOf(2, 2, "v2", "s1"), snapshotOf(3, 3, "v3", "s1")

	tests := []struct {
		name  string
		steps []step
		// wantSaved are the revisions handed to the store, in order, with the content of the flags object
		wantSaved   []uint64
		wantContent []string
		wantPending int
	}{
		{
			name:      "an applied revision is saved",
			steps:     []step{{offer: &one}, {result: round(1, flagruntime.OutcomeApplied), objects: statuses()}},
			wantSaved: []uint64{1}, wantContent: []string{"v1"},
		},
		{
			name: "a rejected revision is not saved, and the next applied one is",
			steps: []step{
				{offer: &one}, {result: round(1, flagruntime.OutcomeApplied), objects: statuses()},
				{offer: &two}, {result: round(2, flagruntime.OutcomeRejected), objects: statuses()},
				{offer: &three}, {result: round(3, flagruntime.OutcomeApplied), objects: statuses()},
			},
			wantSaved: []uint64{1, 3}, wantContent: []string{"v1", "v3"},
		},
		{
			name:  "a rejected first revision saves nothing",
			steps: []step{{offer: &one}, {result: round(1, flagruntime.OutcomeRejected), objects: statuses()}},
		},
		{
			name:  "an ignored revision saves nothing",
			steps: []step{{offer: &one}, {result: round(1, flagruntime.OutcomeIgnored), objects: statuses()}},
		},
		{
			name:        "a round without content saves nothing",
			steps:       []step{{offer: &one}, {result: round(0, flagruntime.OutcomeApplied), objects: statuses()}},
			wantPending: 1,
		},
		{
			name: "the engine keeps the latest: the revisions it dropped are not saved",
			steps: []step{
				{offer: &one}, {offer: &two}, {offer: &three},
				{result: round(3, flagruntime.OutcomeApplied), objects: statuses()},
			},
			wantSaved: []uint64{3}, wantContent: []string{"v3"},
		},
		{
			name:  "a revision that was never offered saves nothing",
			steps: []step{{result: round(9, flagruntime.OutcomeApplied), objects: statuses()}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var saved []manifestreader.Persist

			persist := newPersister(func(p manifestreader.Persist) { saved = append(saved, p) })

			for _, one := range tt.steps {
				if one.offer != nil {
					persist.offer(*one.offer)
				}

				if one.result != nil {
					persist.decided(one.result, one.objects)
				}
			}

			revisions := make([]uint64, 0, len(saved))
			contents := make([]string, 0, len(saved))

			for _, p := range saved {
				revisions = append(revisions, p.Snapshot.Revision)
				contents = append(contents, string(p.Snapshot.Objects[objFlags].Bytes))
			}

			assert.Equal(t, tt.wantSaved, nilIfEmpty(revisions))
			assert.Equal(t, tt.wantContent, nilIfEmpty(contents))
			assert.Len(t, persist.pending, tt.wantPending)
		})
	}
}

func nilIfEmpty[T any](in []T) []T {
	if len(in) == 0 {
		return nil
	}

	return in
}

// A generation applied with an object that the engine could not decode is saved with the content the engine kept for
// the object: the last one that was saved, as a stale object; an object it never held has no content.
func TestPersisterSavesTheLastGoodContentOfAnObjectTheEngineRejected(t *testing.T) {
	t.Parallel()

	good, bad, first := snapshotOf(
		1,
		1,
		"good",
		"s1",
	), snapshotOf(
		2,
		2,
		"broken",
		"s2",
	), snapshotOf(
		1,
		1,
		"broken",
		"s1",
	)

	var saved []manifestreader.Persist

	persist := newPersister(func(p manifestreader.Persist) { saved = append(saved, p) })

	persist.offer(good)
	persist.decided(round(1, flagruntime.OutcomeApplied), statuses())

	persist.offer(bad)
	persist.decided(round(2, flagruntime.OutcomeApplied), statuses(objFlags))

	require.Len(t, saved, 2)

	flags := saved[1].Snapshot.Objects[objFlags]
	assert.Equal(t, manifestreader.StateStale, flags.State, "the object is stale")
	assert.Equal(t, "good", string(flags.Bytes), "with the content the engine serves, not the rejected one")
	assert.Equal(t, good.Snapshot.Objects[objFlags].Checksum, flags.Checksum)
	assert.Equal(t, "s2", string(saved[1].Snapshot.Objects[objSets].Bytes), "the other objects are the new ones")
	assert.Equal(t, "broken", string(bad.Snapshot.Objects[objFlags].Bytes), "the snapshot of the reader is not changed")

	// the same persister, a generation applied with an undecodable object it never held
	fresh := newPersister(func(p manifestreader.Persist) { saved = append(saved, p) })
	fresh.offer(first)
	fresh.decided(round(1, flagruntime.OutcomeApplied), statuses(objFlags))

	require.Len(t, saved, 3)

	none := saved[2].Snapshot.Objects[objFlags]
	assert.Equal(t, manifestreader.StateMissing, none.State)
	assert.Nil(t, none.Bytes)
}

// The snapshots that wait for a decision are bounded.
func TestPersisterForgetsTheOldestPendingSnapshots(t *testing.T) {
	t.Parallel()

	persist := newPersister(func(manifestreader.Persist) {})

	for revision := uint64(1); revision <= maxPending+6; revision++ {
		persist.offer(snapshotOf(revision, revision, "v", "s"))
	}

	assert.Len(t, persist.pending, maxPending)
	assert.NotContains(t, persist.pending, uint64(1))
	assert.Contains(t, persist.pending, uint64(maxPending+6))
}

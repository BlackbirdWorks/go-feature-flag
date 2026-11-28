package relaycore

import (
	"fmt"
	"slices"
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

// body is the content of an object: its bytes are its own checksum, so a test reads which content it holds.
func objectOf(kind, content string) *manifestreader.ObjectState {
	return &manifestreader.ObjectState{
		Kind: kind, Format: "yaml", State: manifestreader.StateOK, Bytes: []byte(content),
		Checksum: controlplane.Checksum{Algorithm: "sha256", Value: content},
	}
}

// staleOf is an object that the reader serves from earlier content of its own.
func staleOf(kind, content string) *manifestreader.ObjectState {
	state := objectOf(kind, content)
	state.State, state.Reason = manifestreader.StateStale, "download"

	return state
}

func snapshotWith(revision uint64, flags, sets *manifestreader.ObjectState) manifestreader.Persist {
	return manifestreader.Persist{
		ETag: "etag", Manifest: []byte("manifest"),
		Snapshot: &manifestreader.Snapshot{
			Revision: revision, Generation: revision,
			Objects: map[string]manifestreader.ObjectState{objFlags: *flags, objSets: *sets},
		},
	}
}

func snapshotOf(revision uint64, flags, sets string) manifestreader.Persist {
	return snapshotWith(revision, objectOf(controlplane.KindFlags, flags), objectOf(controlplane.KindFlagSets, sets))
}

// round is a decided round: Applied with content, or not.
func round(revision uint64, outcome flagruntime.Outcome, content bool) *flagruntime.RoundResult {
	return &flagruntime.RoundResult{
		Outcome: outcome, Code: string(outcome),
		Revision: revision, ContentApplied: content,
	}
}

// held is what the runtime holds: the checksum per object, and its state.
func held(flags, sets string, flagsState flagruntime.ObjectState) map[string]*flagruntime.ObjectStatus {
	return map[string]*flagruntime.ObjectStatus{
		objFlags: {Name: objFlags, Kind: controlplane.KindFlags, State: flagsState, Checksum: flags},
		objSets:  {Name: objSets, Kind: controlplane.KindFlagSets, State: flagruntime.ObjectOK, Checksum: sets},
	}
}

func never() uint64 { return 0 }

// step is one thing that happens to the persister: the reader offers a snapshot, or the engine decides a round.
type step struct {
	offer   *manifestreader.Persist
	result  *flagruntime.RoundResult
	objects map[string]*flagruntime.ObjectStatus
}

// written is what one write handed to the store: the revision and, per object, the state and the content.
type written struct {
	flags, sets string
	flagsState  manifestreader.State
	revision    uint64
}

func writtenOf(persist *manifestreader.Persist) written {
	return written{
		revision: persist.Snapshot.Revision, flags: string(persist.Snapshot.Objects[objFlags].Bytes),
		sets: string(persist.Snapshot.Objects[objSets].Bytes), flagsState: persist.Snapshot.Objects[objFlags].State,
	}
}

// The state directory is handed what the runtime serves, once the engine stored the content, whichever of the offer
// of the snapshot and the decision of the round comes first.
func TestPersisterWritesWhatTheRuntimeServes(t *testing.T) {
	t.Parallel()

	one, two, three := snapshotOf(1, "f1", "s1"), snapshotOf(2, "f2", "s1"), snapshotOf(3, "f3", "s1")
	// the generation 3 could not download its flags object: the reader serves the bytes of the rejected generation 2
	downloadFailed := snapshotWith(3, staleOf(controlplane.KindFlags, "f2"), objectOf(controlplane.KindFlagSets, "s1"))
	ok, stale, invalid := flagruntime.ObjectOK, flagruntime.ObjectStale, flagruntime.ObjectInvalid

	tests := []struct {
		name        string
		steps       []step
		wantWritten []written
		wantPending int
	}{
		{
			name: "an applied revision is written",
			steps: []step{
				{offer: &one},
				{result: round(1, flagruntime.OutcomeApplied, true), objects: held("f1", "s1", ok)},
			},
			wantWritten: []written{{revision: 1, flags: "f1", sets: "s1", flagsState: manifestreader.StateOK}},
		},
		{
			name: "the decision before the offer: the write is made when the snapshot comes",
			steps: []step{
				{result: round(1, flagruntime.OutcomeApplied, true), objects: held("f1", "s1", ok)},
				{offer: &one},
			},
			wantWritten: []written{{revision: 1, flags: "f1", sets: "s1", flagsState: manifestreader.StateOK}},
		},
		{
			name: "a rejected revision is not written, and the next applied one is",
			steps: []step{
				{offer: &one}, {result: round(1, flagruntime.OutcomeApplied, true), objects: held("f1", "s1", ok)},
				{offer: &two}, {result: round(2, flagruntime.OutcomeRejected, false), objects: held("f1", "s1", ok)},
				{offer: &three}, {result: round(3, flagruntime.OutcomeApplied, true), objects: held("f3", "s1", ok)},
			},
			wantWritten: []written{
				{revision: 1, flags: "f1", sets: "s1", flagsState: manifestreader.StateOK},
				{revision: 3, flags: "f3", sets: "s1", flagsState: manifestreader.StateOK},
			},
		},
		{
			name: "a rejected first revision writes nothing",
			steps: []step{
				{offer: &one},
				{result: round(1, flagruntime.OutcomeRejected, false), objects: held("", "", ok)},
			},
		},
		{
			name: "a bootstrap change that came with rejected content writes nothing of the content",
			steps: []step{
				{offer: &one}, {result: round(1, flagruntime.OutcomeApplied, true), objects: held("f1", "s1", ok)},
				{offer: &two}, {result: round(2, flagruntime.OutcomeApplied, false), objects: held("f1", "s1", ok)},
			},
			wantWritten: []written{{revision: 1, flags: "f1", sets: "s1", flagsState: manifestreader.StateOK}},
		},
		{
			name: "an ignored revision writes nothing",
			steps: []step{
				{offer: &one},
				{result: round(1, flagruntime.OutcomeIgnored, false), objects: held("f1", "s1", ok)},
			},
		},
		{
			name: "a round without content writes nothing",
			steps: []step{
				{offer: &one},
				{result: round(0, flagruntime.OutcomeApplied, false), objects: held("f1", "s1", ok)},
			},
			wantPending: 1,
		},
		{
			name: "the engine keeps the latest: the revisions it dropped are not written",
			steps: []step{
				{offer: &one}, {offer: &two}, {offer: &three},
				{result: round(3, flagruntime.OutcomeApplied, true), objects: held("f3", "s1", ok)},
			},
			wantWritten: []written{{revision: 3, flags: "f3", sets: "s1", flagsState: manifestreader.StateOK}},
		},
		{
			name:        "a revision that the engine never decided is not written (a crash before the decision)",
			steps:       []step{{offer: &one}, {offer: &two}},
			wantPending: 2,
		},
		{
			name:  "a revision that was never offered writes nothing",
			steps: []step{{result: round(9, flagruntime.OutcomeApplied, true), objects: held("f1", "s1", ok)}},
		},
		{
			name: "after a rejected generation, a failed download writes the bytes the runtime holds, not the reader's",
			steps: []step{
				{offer: &one},
				{result: round(1, flagruntime.OutcomeApplied, true), objects: held("f1", "s1", ok)},
				{offer: &two},
				{result: round(2, flagruntime.OutcomeRejected, false), objects: held("f1", "s1", ok)},
				{
					offer: &downloadFailed,
				},
				{result: round(3, flagruntime.OutcomeApplied, true), objects: held("f1", "s1", stale)},
			},
			wantWritten: []written{
				{revision: 1, flags: "f1", sets: "s1", flagsState: manifestreader.StateOK},
				{revision: 3, flags: "f1", sets: "s1", flagsState: manifestreader.StateStale},
			},
		},
		{
			name: "an object the engine could not decode is written with the content the runtime holds",
			steps: []step{
				{offer: &one}, {result: round(1, flagruntime.OutcomeApplied, true), objects: held("f1", "s1", ok)},
				{offer: &two}, {result: round(2, flagruntime.OutcomeApplied, true), objects: held("f1", "s1", invalid)},
			},
			wantWritten: []written{
				{revision: 1, flags: "f1", sets: "s1", flagsState: manifestreader.StateOK},
				{revision: 2, flags: "f1", sets: "s1", flagsState: manifestreader.StateStale},
			},
		},
		{
			name: "an object the runtime never held is written as missing",
			steps: []step{
				{offer: &one}, {result: round(1, flagruntime.OutcomeApplied, true), objects: held("", "s1", invalid)},
			},
			wantWritten: []written{{revision: 1, flags: "", sets: "s1", flagsState: manifestreader.StateMissing}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var saved []written

			persist := newPersister(func(p manifestreader.Persist) { saved = append(saved, writtenOf(&p)) }, never)

			for _, one := range tt.steps {
				if one.offer != nil {
					persist.offer(*one.offer)
				}

				if one.result != nil {
					persist.decided(one.result, one.objects)
				}
			}

			assert.Equal(t, nilIfEmpty(tt.wantWritten), nilIfEmpty(saved))
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

// What is written names the checksum the runtime holds, with the manifest of the generation it runs.
func TestPersisterWritesTheHeldChecksumAndTheManifest(t *testing.T) {
	t.Parallel()

	one := snapshotOf(1, "f1", "s1")
	downloadFailed := snapshotWith(2, staleOf(controlplane.KindFlags, "f2"), objectOf(controlplane.KindFlagSets, "s1"))
	downloadFailed.Manifest = []byte("manifest 2")

	var saved []manifestreader.Persist

	persist := newPersister(func(p manifestreader.Persist) { saved = append(saved, p) }, never)

	persist.offer(one)
	persist.decided(round(1, flagruntime.OutcomeApplied, true), held("f1", "s1", flagruntime.ObjectOK))
	persist.offer(downloadFailed)
	persist.decided(round(2, flagruntime.OutcomeApplied, true), held("f1", "s1", flagruntime.ObjectStale))

	require.Len(t, saved, 2)

	flags := saved[1].Snapshot.Objects[objFlags]
	assert.Equal(t, "f1", flags.Checksum.Value, "the checksum the runtime holds")
	assert.Equal(t, "sha256", flags.Checksum.Algorithm)
	assert.Equal(t, "manifest 2", string(saved[1].Manifest), "the manifest of the generation the runtime runs")
	assert.Equal(
		t,
		"f2",
		string(downloadFailed.Snapshot.Objects[objFlags].Bytes),
		"the snapshot of the reader is not changed",
	)
}

// The engine decides the revision it took, never an older one: the snapshots older than the one taken are forgotten as
// they are offered, the one in flight and the newer ones are kept until they are decided, whatever their number.
func TestPersisterKeepsTheRevisionInFlight(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		offered     []uint64
		wantPending []uint64
		wantSaved   []uint64
		taken       uint64
		decide      uint64
	}{
		{
			name: "older than the taken one are forgotten", taken: 3, offered: []uint64{1, 2, 3},
			decide: 3, wantSaved: []uint64{3},
		},
		{
			name: "the one in flight survives newer offers", taken: 2, offered: []uint64{1, 2, 3, 4},
			decide: 2, wantPending: []uint64{3, 4}, wantSaved: []uint64{2},
		},
		{
			name: "a long run of newer offers does not drop the one in flight", taken: 1,
			offered: seq(1, maxPending), decide: 1, wantPending: seq(2, maxPending), wantSaved: []uint64{1},
		},
		{
			name: "the guard of a stalled engine forgets the oldest", taken: 0,
			offered: seq(1, maxPending+6), decide: maxPending + 6, wantSaved: []uint64{maxPending + 6},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var saved []uint64

			persist := newPersister(
				func(p manifestreader.Persist) { saved = append(saved, p.Snapshot.Revision) },
				func() uint64 { return tt.taken },
			)

			for _, revision := range tt.offered {
				persist.offer(snapshotOf(revision, "f", "s"))
			}

			persist.decided(round(tt.decide, flagruntime.OutcomeApplied, true), held("f", "s", flagruntime.ObjectOK))

			pending := make([]uint64, 0, len(persist.pending))
			for revision := range persist.pending {
				pending = append(pending, revision)
			}

			slices.Sort(pending)

			assert.Equal(t, tt.wantSaved, saved)
			assert.Equal(t, nilIfEmpty(tt.wantPending), nilIfEmpty(pending))
		})
	}
}

func seq(from, to uint64) []uint64 {
	out := make([]uint64, 0, to-from+1)
	for revision := from; revision <= to; revision++ {
		out = append(out, revision)
	}

	return out
}

// The work of a write grows with the objects of the manifest, not with their square: every object is a lookup in an
// index made once.
func TestPersisterLookupsDoNotScaleWithTheObjects(t *testing.T) {
	t.Parallel()

	for _, size := range []int{50, 500, 5000} {
		t.Run(fmt.Sprintf("%d objects", size), func(t *testing.T) {
			t.Parallel()

			objects := make(map[string]manifestreader.ObjectState, size)
			status := make(map[string]*flagruntime.ObjectStatus, size)

			for i := range size {
				name, sum := fmt.Sprintf("obj-%d", i), fmt.Sprintf("sum-%d", i)
				objects[name] = *objectOf(controlplane.KindFlags, sum)
				status[name] = &flagruntime.ObjectStatus{
					Name: name, Kind: controlplane.KindFlags, State: flagruntime.ObjectOK, Checksum: sum,
				}
			}

			persist := newPersister(func(manifestreader.Persist) {}, never)
			served := persist.served(&manifestreader.Snapshot{Objects: objects}, status)

			assert.Len(t, served.Objects, size)
			assert.Equal(t, uint64(size), persist.visits, "one visit per object of the snapshot, none per lookup")
		})
	}
}

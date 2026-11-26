package controlsource_test

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/controlsource"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/internal/manifestreader"
	"github.com/blackbirdworks/semaphore/modules/core/testhelpers/clocktest"
)

const (
	keyA        = "key-a"
	keyB        = "key-b"
	objA        = "flags-a"
	objB        = "flags-b"
	docName     = "sets"
	objC        = "flags-c"
	objShared   = "shared"
	secretKey   = "canary-5d1c-secret"
	secretAdmin = "canary-admin-77aa"
)

// Content maps every state and reason of a snapshot one to one, shares the bytes and keeps the identity of the
// object (kind, format, checksum).
func TestContent_MapsEveryState(t *testing.T) {
	t.Parallel()

	data := flagsYAML(1, "f")
	sum := controlplane.ChecksumOf(data)

	tests := []struct {
		name       string
		wantState  flagruntime.ObjectState
		wantReason string
		in         manifestreader.ObjectState
		wantBytes  bool
	}{
		{name: "ok", in: flagsObject(data), wantState: flagruntime.ObjectOK, wantBytes: true},
		{
			name:      "stale keeps its bytes and its own checksum",
			in:        failed(data, manifestreader.StateStale, manifestreader.ReasonChecksumMismatch),
			wantState: flagruntime.ObjectStale, wantReason: manifestreader.ReasonChecksumMismatch, wantBytes: true,
		},
		{
			name:      "stale because of an error",
			in:        failed(data, manifestreader.StateStale, "error"),
			wantState: flagruntime.ObjectStale, wantReason: "error", wantBytes: true,
		},
		{
			name:      "missing",
			in:        failed(data, manifestreader.StateMissing, manifestreader.ReasonMissing),
			wantState: flagruntime.ObjectMissing, wantReason: manifestreader.ReasonMissing,
		},
		{
			name:      "missing, not restored",
			in:        failed(data, manifestreader.StateMissing, manifestreader.ReasonNotRestored),
			wantState: flagruntime.ObjectMissing, wantReason: manifestreader.ReasonNotRestored,
		},
		{
			name:      "missing, too large",
			in:        failed(data, manifestreader.StateMissing, manifestreader.ReasonTooLarge),
			wantState: flagruntime.ObjectMissing, wantReason: manifestreader.ReasonTooLarge,
		},
		{
			name:      "a state this version does not know is missing",
			in:        failed(data, manifestreader.State("future"), "later"),
			wantState: flagruntime.ObjectMissing, wantReason: "later", wantBytes: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := controlsource.Content(snapshot(9, map[string]manifestreader.ObjectState{objA: tt.in}))

			assert.EqualValues(t, 9, got.Generation)
			require.Contains(t, got.Objects, objA)

			obj := got.Objects[objA]
			assert.Equal(t, flagruntime.KindFlags, obj.Kind)
			assert.Equal(t, controlplane.FormatYAML, obj.Format)
			assert.Equal(t, sum.Value, obj.Checksum)
			assert.Equal(t, tt.wantState, obj.State)
			assert.Equal(t, tt.wantReason, obj.Reason)

			if !tt.wantBytes {
				assert.Nil(t, obj.Bytes)

				return
			}

			require.NotEmpty(t, obj.Bytes)
			// zero copy: the content shares the bytes of the snapshot
			assert.Same(t, &tt.in.Bytes[0], &obj.Bytes[0])
		})
	}
}

// The snapshot maps to the same content for the empty snapshot.
func TestContent_EmptySnapshot(t *testing.T) {
	t.Parallel()

	got := controlsource.Content(snapshot(3, nil))

	assert.EqualValues(t, 3, got.Generation)
	assert.Empty(t, got.Objects)
}

// A burst of snapshots reaches the engine as its newest one: the engine builds the last generation only. The source
// takes every snapshot while the engine is not running; the engine then runs one round, for the last generation,
// and the next snapshot is the next round.
func TestSource_LatestWinsBurst(t *testing.T) {
	t.Parallel()

	harness := newRig(t)

	objects := func(value int) map[string]manifestreader.ObjectState {
		return map[string]manifestreader.ObjectState{
			objA:    flagsObject(flagsYAML(value, "f")),
			docName: setsObject(t, fset("s", []string{objA}, keyA)),
		}
	}

	for gen := 1; gen <= 5; gen++ {
		harness.reader.send(t, snapshot(uint64(gen), objects(gen)))
	}

	assert.Equal(t, []uint64{1, 2, 3, 4, 5}, harness.submitted(5), "the source hands every snapshot over, in order")

	harness.startEngine()

	res := harness.nextRound()
	assert.Equal(t, flagruntime.OutcomeApplied, res.Outcome, res.Detail)
	assert.EqualValues(t, 5, res.Generation, "only the newest generation is built")
	assert.EqualValues(t, 1, harness.holder.Load().Snapshot, "one swap for the whole burst")

	next := harness.feed(snapshot(6, objects(6)))
	assert.EqualValues(t, 6, next.Generation)
	assert.EqualValues(t, 2, harness.holder.Load().Snapshot, "no round ran for the generations that were replaced")

	// the replaced generations were never parsed: the flags object was parsed for generation 5 and for generation 6
	var parsedFlags []string

	for _, event := range harness.parses() {
		if event.Kind == flagruntime.KindFlags {
			parsedFlags = append(parsedFlags, event.Name)
		}
	}

	assert.Equal(t, []string{objA, objA}, parsedFlags)
}

// A snapshot becomes a served runtime: the key routes, the flags evaluate, a stale object keeps the previous flags
// and a missing object with no earlier definition leaves a pending (placeholder) flag set whose key still routes.
func TestSource_StatesReachTheRuntime(t *testing.T) {
	t.Parallel()

	harness := newRig(t)
	harness.startEngine()

	flagsA := flagsYAML(1, "f")
	flagsB := flagsYAML(2, "g")
	doc := setsObject(t, fset("a", []string{objA}, keyA), fset("b", []string{objB}, keyB))

	res := harness.feed(snapshot(1, map[string]manifestreader.ObjectState{
		objA: flagsObject(flagsA), objB: flagsObject(flagsB), docName: doc,
	}))
	require.Equal(t, flagruntime.OutcomeApplied, res.Outcome, res.Detail)

	run := harness.holder.Load()
	setA, route := run.Route(keyA)
	require.Equal(t, flagruntime.RouteFlagSet, route)
	assert.Len(t, setA.Flags, 1)

	// the object of a goes stale: its flags stay, the flag set is degraded; b is untouched
	res = harness.feed(snapshot(1, map[string]manifestreader.ObjectState{
		objA:    failed(flagsA, manifestreader.StateStale, manifestreader.ReasonChecksumMismatch),
		objB:    flagsObject(flagsB),
		docName: doc,
	}))
	require.Equal(t, flagruntime.OutcomeApplied, res.Outcome, res.Detail)

	run = harness.holder.Load()
	staleA, _ := run.Route(keyA)
	assert.Len(t, staleA.Flags, 1, "a stale object keeps the previous flags")
	assert.Equal(t, flagruntime.StateDegraded, staleA.State)
	assert.Equal(t, flagruntime.ObjectStale, run.Status.Objects[objA].State)
	assert.Equal(t, manifestreader.ReasonChecksumMismatch, run.Status.Objects[objA].Reason)

	liveB, _ := run.Route(keyB)
	assert.Equal(t, flagruntime.StateServing, liveB.State)

	// generation 2 adds a flag set whose only object is missing and was never loaded: a placeholder
	pending := setsObject(t, fset("a", []string{objA}, keyA), fset("b", []string{objB}, keyB),
		fset("c", []string{objC}, "key-c"))

	res = harness.feed(snapshot(2, map[string]manifestreader.ObjectState{
		objA:    flagsObject(flagsA),
		objB:    flagsObject(flagsB),
		docName: pending,
		objC: failed(
			flagsYAML(3, "h"),
			manifestreader.StateMissing,
			manifestreader.ReasonNotRestored,
		),
	}))
	require.Equal(t, flagruntime.OutcomeApplied, res.Outcome, res.Detail)

	run = harness.holder.Load()
	setC, route := run.Route("key-c")
	assert.Equal(t, flagruntime.RoutePending, route)
	assert.Empty(t, setC.Flags)
	assert.Equal(t, flagruntime.ObjectMissing, run.Status.Objects[objC].State)
}

// One changed object rebuilds only the flag sets that use it: the others keep their pointer, and an object is parsed
// once per checksum, whatever the number of flag sets that list it.
func TestSource_StructuralSharing(t *testing.T) {
	t.Parallel()

	harness := newRig(t)
	harness.startEngine()

	flagsA, flagsB, shared := flagsYAML(1, "f"), flagsYAML(2, "g"), flagsYAML(3, "h")
	doc := setsObject(t,
		fset("a", []string{objA}, keyA),
		fset("b", []string{objB}, keyB),
		fset("s1", []string{objShared}, "k-s1"),
		fset("s2", []string{objShared}, "k-s2"))
	objects := func(a []byte) map[string]manifestreader.ObjectState {
		return map[string]manifestreader.ObjectState{
			objA: flagsObject(a), objB: flagsObject(flagsB), objShared: flagsObject(shared), docName: doc,
		}
	}

	harness.feed(snapshot(1, objects(flagsA)))

	first := harness.holder.Load()
	assert.Equal(t, []string{objA, objB, docName, objShared}, parsedNames(harness.parses()),
		"three flags objects and one document, each parsed once")

	harness.feed(snapshot(2, objects(flagsYAML(10, "f"))))

	second := harness.holder.Load()

	for _, name := range []string{"b", "s1", "s2"} {
		assert.Same(t, first.FlagSets[name], second.FlagSets[name], "%s is untouched", name)
	}

	assert.NotSame(t, first.FlagSets["a"], second.FlagSets["a"], "the flag set of the changed object is rebuilt")
	assert.Equal(t, 10, mustVariation(t, second.FlagSets["a"], "f"))

	assert.Equal(t, []string{objA}, parsedNames(harness.parses()), "one object parsed for one changed checksum")
}

func mustVariation(tb testing.TB, set *flagruntime.FlagSet, name string) any {
	tb.Helper()

	flag, ok := set.Flag(name)
	require.True(tb, ok)

	return flag.GetVariationValue("v")
}

// The source stops when its context ends and when the reader closes its channel; nothing it started is left (the
// source starts no goroutine, so the labelled goroutine count is zero once Run returned).
func TestSource_StopsWithoutLeaks(t *testing.T) {
	t.Parallel()

	t.Run("context", func(t *testing.T) {
		t.Parallel()

		harness := newRig(t)

		// a handed over snapshot proves that the goroutine runs under its label
		harness.reader.send(t, snapshot(1, nil))
		harness.submitted(1)

		assert.Equal(t, 1, harness.labelled(), "the source is one goroutine")

		harness.stop()

		assert.Zero(t, harness.labelled())
	})

	t.Run("reader closed", func(t *testing.T) {
		t.Parallel()

		harness := newRig(t)

		close(harness.reader.snaps)

		<-harness.srcEnd

		assert.Zero(t, harness.labelled())
	})
}

// A nil snapshot is ignored.
func TestSource_NilSnapshotIgnored(t *testing.T) {
	t.Parallel()

	harness := newRig(t)

	harness.reader.send(t, nil)
	harness.reader.send(t, snapshot(1, nil))

	assert.Equal(t, []uint64{1}, harness.submitted(1))
	assert.EqualValues(t, 1, harness.source.Status().Received)
}

// The joined status holds the reader status, the engine result and the status tree of the running runtime, and no
// key and no object bytes: the canary key of a flag set is searched in its JSON.
func TestSource_StatusJoinsAndHidesKeys(t *testing.T) {
	t.Parallel()

	harness := newRig(t)
	harness.reader.status = manifestreader.Status{
		Polls: 4,
		Poll:  manifestreader.PollStatus{Result: manifestreader.PollUpdated},
		Manifest: manifestreader.ManifestStatus{
			Outcome: manifestreader.OutcomeAccepted, Generation: 7, Objects: 2,
		},
		Failures: []manifestreader.ObjectFailure{{Name: objB, State: manifestreader.StateStale, Reason: "error"}},
	}

	before := harness.source.Status()
	assert.Zero(t, before.Received)
	assert.NotNil(t, before.Runtime, "the empty runtime has a status")
	assert.Equal(t, flagruntime.RoundResult{}, before.Round)

	harness.startEngine()
	harness.feed(snapshot(7, map[string]manifestreader.ObjectState{
		objA:    flagsObject(flagsYAML(1, "f")),
		docName: setsObject(t, fset("a", []string{objA}, secretKey)),
	}))

	status := harness.source.Status()

	assert.EqualValues(t, 1, status.Received)
	assert.EqualValues(t, 7, status.LastGeneration)
	assert.EqualValues(t, 7, status.Round.Generation)
	assert.Equal(t, flagruntime.OutcomeApplied, status.Round.Outcome)
	assert.EqualValues(t, 7, status.Runtime.Generation)
	assert.EqualValues(t, 4, status.Reader.Polls)
	assert.Equal(t, objB, status.Reader.Failures[0].Name)
	assert.Equal(t, 1, status.Runtime.FlagSets["a"].APIKeys)

	encoded, err := json.Marshal(status)
	require.NoError(t, err)

	for _, secret := range []string{secretKey, secretAdmin} {
		assert.NotContains(t, string(encoded), secret)
	}

	assert.Contains(t, string(encoded), `"generation":7`)
}

// The JSON of the joined status is pinned: camelCase names, the reader status, the engine result and the runtime
// tree, with the same names everywhere. No API key, admin key or object byte is in it (canaries in the flag set
// keys, the admin keys and the object content).
func TestSource_StatusJSONGolden(t *testing.T) {
	t.Parallel()

	harness := newRig(t)
	harness.reader.status = manifestreader.Status{
		Polls:    4,
		Poll:     manifestreader.PollStatus{At: clocktest.Instant(), Result: manifestreader.PollUpdated},
		Manifest: manifestreader.ManifestStatus{Outcome: manifestreader.OutcomeAccepted, Generation: 7, Objects: 2},
		Counts:   manifestreader.Counts{OK: 1, Stale: 1},
		Failures: []manifestreader.ObjectFailure{{
			Name: objB, State: manifestreader.StateStale, Reason: manifestreader.ReasonChecksumMismatch,
			Checksum: "sha256:abcd", Retryable: true,
		}},
	}

	harness.startEngine()

	var cfg bootstrap.Config

	require.NoError(t, yaml.Unmarshal([]byte("auth:\n  adminKeys:\n    - "+secretAdmin+"\n"), &cfg))
	harness.engine.Reload(&cfg)
	harness.nextRound()

	flags := flagsYAML(1, "f")

	harness.feed(snapshot(7, map[string]manifestreader.ObjectState{
		objA: flagsObject(append(flags, []byte("# "+secretKey+"\n")...)),
		docName: setsObject(
			t,
			fset("a", []string{objA}, secretKey, secretKey+"-2"),
			fset("b", []string{objC}, "key-b"),
		),
	}))

	got, err := json.Marshal(harness.source.Status())
	require.NoError(t, err)

	want, err := os.ReadFile("testdata/status.golden.json")
	require.NoError(t, err)

	assert.JSONEq(t, string(want), string(got))

	for _, secret := range []string{secretKey, secretAdmin} {
		assert.NotContains(t, string(got), secret)
	}
}

// A name that changes kind while its new object is failing recovers at the same generation: the snapshot of the
// failure carries the old bytes with the old kind (stale), the snapshot of the recovery carries the new object with
// its own kind, and the runtime installs the routes of the new document without a third generation.
func TestSource_KindChangeRecoversAtTheSameGeneration(t *testing.T) {
	t.Parallel()

	harness := newRig(t)
	harness.startEngine()

	oldFlags := flagsYAML(1, "f")
	doc := setsObject(t, fset("a", []string{objA}, keyA))
	moved := setsObject(t, fset("moved", []string{objA}, "key-moved"))

	res := harness.feed(snapshot(1, map[string]manifestreader.ObjectState{
		objA: flagsObject(oldFlags), objShared: flagsObject(oldFlags), docName: doc,
	}))
	require.Equal(t, flagruntime.OutcomeApplied, res.Outcome, res.Detail)

	// generation 2 lists "shared" as a flag-set document that cannot be fetched yet
	res = harness.feed(snapshot(2, map[string]manifestreader.ObjectState{
		objA:      flagsObject(oldFlags),
		objShared: failed(oldFlags, manifestreader.StateStale, manifestreader.ReasonMissing),
		docName:   doc,
	}))
	require.Equal(t, flagruntime.OutcomeApplied, res.Outcome, res.Detail)

	// the object is there at the next poll: same generation, new kind
	res = harness.feed(snapshot(2, map[string]manifestreader.ObjectState{
		objA: flagsObject(oldFlags), objShared: moved, docName: doc,
	}))
	assert.Equal(t, flagruntime.OutcomeApplied, res.Outcome, res.Detail)

	_, route := harness.holder.Load().Route("key-moved")
	assert.Equal(t, flagruntime.RouteFlagSet, route)
}

// The generation of the status is recorded before the engine can answer: while the engine still holds the snapshot,
// the status already names its generation, so a reader of the status that has seen the round has seen the generation.
func TestSource_LastGenerationIsRecordedBeforeTheHandOver(t *testing.T) {
	t.Parallel()

	reader := newFakeReader()
	engine := &gatedEngine{entered: make(chan uint64, 1), release: make(chan struct{})}
	source := controlsource.New(reader, engine, flagruntime.NewHolder(), nil)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})

	go func() {
		defer close(done)

		source.Run(ctx)
	}()

	t.Cleanup(func() {
		cancel()
		close(engine.release)
		<-done
	})

	reader.send(t, snapshot(5, nil))

	timer := time.NewTimer(waitBound)
	defer timer.Stop()

	select {
	case gen := <-engine.entered:
		assert.EqualValues(t, 5, gen)
		assert.EqualValues(t, 5, source.Status().LastGeneration, "recorded while the engine holds the snapshot")
		assert.EqualValues(t, 1, source.Status().Received)
	case <-timer.C:
		require.FailNow(t, "the source did not hand the snapshot over")
	}
}

// gatedEngine holds every Submit until the test releases it.
type gatedEngine struct {
	entered chan uint64
	release chan struct{}
}

func (g *gatedEngine) Submit(content *flagruntime.Content) {
	g.entered <- content.Generation

	<-g.release
}

func (*gatedEngine) Result() flagruntime.RoundResult { return flagruntime.RoundResult{} }

// parsedNames lists the names of the parsed objects, sorted.
func parsedNames(events []flagruntime.ParseEvent) []string {
	names := make([]string, 0, len(events))
	for _, event := range events {
		names = append(names, event.Name)
	}

	slices.Sort(names)

	return names
}

package runtime_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	goruntime "runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/runtime"
	"github.com/blackbirdworks/semaphore/controlplane"
)

// generationContent builds a content in which every flag of every flag set, and every API key, carries the
// generation: a reader that sees two generations in one runtime sees two numbers.
func generationContent(t *testing.T, gen uint64, sets int) *runtime.Content {
	t.Helper()

	objects := objs{"common": flagsObj(int(gen), "c", "d")}
	flagSets := make([]controlplane.FlagSet, 0, sets)

	for i := range sets {
		object := fmt.Sprintf("own-%d", i)
		objects[object] = flagsObj(int(gen), "a", "b")
		flagSets = append(
			flagSets,
			fset(fmt.Sprintf("s%d", i), src("common", object), fmt.Sprintf("key-%d-s%d", gen, i)),
		)
	}

	objects[setsName] = setsObj(t, flagSets...)

	return content(gen, objects)
}

func TestRuntimeSwapIsAtomicForReaders(t *testing.T) {
	t.Parallel()

	const (
		readers = 4
		gens    = 30
		sets    = 6
	)

	r := newRig(t, runtime.Hooks{})
	r.start()

	var (
		stop     atomic.Bool
		wg       sync.WaitGroup
		latest   [readers]atomic.Uint64
		seenGens [readers]map[uint64]struct{}
	)

	for reader := range readers {
		seenGens[reader] = map[uint64]struct{}{}

		wg.Go(func() {
			var lastSnapshot uint64

			for !stop.Load() {
				current := r.holder.Load()

				assert.GreaterOrEqual(t, current.Snapshot, lastSnapshot, "the snapshot never decreases")

				lastSnapshot = current.Snapshot

				checkOneGeneration(t, current, sets)

				seenGens[reader][current.Generation] = struct{}{}
				latest[reader].Store(current.Generation)
			}
		})
	}

	for gen := uint64(1); gen <= gens; gen++ {
		res := r.feed(generationContent(t, gen, sets))

		require.Equal(t, runtime.OutcomeApplied, res.Outcome)

		// the next generation is fed when every reader has seen this one, so every reader sees every generation
		for reader := range readers {
			for latest[reader].Load() < gen {
				goruntime.Gosched()
			}
		}
	}

	stop.Store(true)
	wg.Wait()

	for reader := range readers {
		for gen := uint64(1); gen <= gens; gen++ {
			assert.Contains(t, seenGens[reader], gen, "reader %d saw generation %d", reader, gen)
		}
	}
}

// checkOneGeneration asserts that everything reachable from rt belongs to the generation of rt.
func checkOneGeneration(t *testing.T, current *runtime.Runtime, sets int) {
	t.Helper()

	if current.Generation == 0 {
		assert.Empty(t, current.FlagSets)
		assert.Empty(t, current.ByKey)

		return
	}

	assert.Len(t, current.FlagSets, sets)
	assert.Len(t, current.ByKey, sets)

	for name, set := range current.FlagSets {
		for _, flagName := range []string{"a", "b", "c", "d"} {
			assert.EqualValues(t, current.Generation, value(set, flagName), "flag %s of %s", flagName, name)
		}
	}

	for key, set := range current.ByKey {
		assert.Equal(
			t,
			fmt.Sprintf("key-%d-%s", current.Generation, set.Name),
			key,
			"the key route belongs to the generation",
		)
		assert.Same(t, current.FlagSets[set.Name], set)
	}

	assert.Equal(t, current.Generation, current.Status.Generation)
	assert.Equal(t, current.Snapshot, current.Status.Snapshot)
}

func TestSwapKeepsAtMostTwoVersionsOfAChangedFlagSet(t *testing.T) {
	t.Parallel()

	const (
		changed   = 5
		unchanged = 5
	)

	var (
		gated      atomic.Bool
		candidates = make(chan *runtime.Runtime)
		release    = make(chan struct{})
	)

	r := newRig(t, runtime.Hooks{BeforeSwap: func(candidate *runtime.Runtime) error {
		if gated.Load() {
			candidates <- candidate

			<-release
		}

		return nil
	}})
	r.start()

	flagSets := make([]controlplane.FlagSet, 0, changed+unchanged)
	for i := range changed {
		flagSets = append(flagSets, fset(fmt.Sprintf("hot-%d", i), src("hot"), fmt.Sprintf("key-hot-%d", i)))
	}

	for i := range unchanged {
		flagSets = append(flagSets, fset(fmt.Sprintf("cold-%d", i), src("cold"), fmt.Sprintf("key-cold-%d", i)))
	}

	base := objs{"hot": flagsObj(1, "x", "y"), "cold": flagsObj(1, "p", "q"), setsName: setsObj(t, flagSets...)}

	r.feed(content(1, base))

	old := r.holder.Load()

	gated.Store(true)
	r.eng.Submit(content(2, base.with(objs{"hot": flagsObj(2, "x", "y")})))

	// the engine is now inside BeforeSwap: the candidate is built, the store has not happened
	candidate := <-candidates

	assert.Same(t, old, r.holder.Load(), "readers still see the old runtime while the candidate is built")

	distinct := map[*runtime.FlagSet]struct{}{}

	for _, fs := range old.FlagSets {
		distinct[fs] = struct{}{}
	}

	for _, fs := range candidate.FlagSets {
		distinct[fs] = struct{}{}
	}

	assert.Len(t, distinct, changed+unchanged+changed, "two versions of the changed flag sets, one of the unchanged")

	for name, oldSet := range old.FlagSets {
		newSet := candidate.FlagSets[name]

		if strings.HasPrefix(name, "hot-") {
			assert.NotSame(t, oldSet, newSet, name)

			continue
		}

		assert.Same(t, oldSet, newSet, name)

		for flagName := range oldSet.Flags {
			assert.Same(t, oldSet.Flags[flagName], newSet.Flags[flagName], "%s/%s exists once", name, flagName)
		}
	}

	require.Len(t, r.parsed, 4, "the first generation parsed three objects, the second one only the changed object")
	assert.Equal(t, "hot", r.parsed[3].Name)

	release <- struct{}{}

	res := <-r.rounds

	assert.Equal(t, runtime.OutcomeApplied, res.Outcome)
	assert.Same(t, candidate, r.holder.Load())
	assert.Len(t, r.parsed, 4, "the swap parses nothing more")
}

type syncBuffer struct {
	buf bytes.Buffer
	mu  sync.Mutex
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

func TestNoSecretInAnyOutput(t *testing.T) {
	t.Parallel()

	const (
		apiKey      = "CANARY-API-KEY-1"
		dupKey      = "CANARY-API-KEY-DUP"
		badKey      = "CANARY-API-KEY-BAD"
		adminKey    = "CANARY-ADMIN-KEY-1"
		envValue    = "CANARY-ENV-VALUE"
		canaryStamp = "CANARY"
	)

	logs := &syncBuffer{}
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	r := newRigLogger(t, logger, runtime.Hooks{}, runtime.WithSecretOptions(bootstrap.LoadOptions{
		LookupEnv: func(name string) (string, bool) { return envValue, name == "KNOWN" },
	}))
	r.start()

	var details []string

	collect := func(res runtime.Reload) { details = append(details, res.Detail) }

	collect(r.reload(bootstrapFromYAML(t, "version: 2\nauth:\n  adminKeys: ["+adminKey+", {fromEnv: KNOWN}]\n")))
	collect(r.reload(bootstrapFromYAML(t, "version: 2\nauth:\n  adminKeys: [{fromEnv: NOPE}]\n")))

	published := objs{
		"f1": flagsObj(1, "x"),
		setsName: setsObj(t,
			fset("a", src("f1"), apiKey, dupKey),
			fset("b", src("f1"), dupKey),
			fset("c", nil, badKey),
			fset("d", src("missing"), badKey+"-2"),
		),
	}

	collect(r.feed(content(1, published)))
	collect(r.feed(content(1, published.with(objs{"f1": flagsObj(2, "x")}))))
	collect(r.feed(content(0, published)))

	current := r.holder.Load()

	// the canaries are real: they route, so a leak would be possible
	_, routed := current.FlagSetByKey(apiKey)
	assert.True(t, routed)
	assert.True(t, current.IsAdminKey(adminKey))
	assert.True(t, current.IsAdminKey(envValue))

	problems := make([]controlplane.Code, 0, len(current.Status.Entries))

	for _, entry := range current.Status.Entries {
		problems = append(problems, entry.Code)
	}

	assert.Contains(t, problems, controlplane.CodeDuplicateKey, "the conflict is reported")
	assert.Contains(t, problems, controlplane.CodeMissingValue)
	assert.Contains(t, problems, controlplane.CodeUnresolvedSource)

	statusJSON, err := json.Marshal(current.Status)
	require.NoError(t, err)

	outputs := map[string]string{
		"logs":        logs.String(),
		"status %+v":  fmt.Sprintf("%+v", current.Status),
		"status %v":   fmt.Sprintf("%v", *current.Status),
		"status json": string(statusJSON),
		"details":     strings.Join(details, "\n"),
		"result":      fmt.Sprintf("%+v", r.eng.Result()),
	}

	for name, rec := range current.Status.FlagSets {
		outputs["flag set status "+name] = fmt.Sprintf("%+v", *rec)
	}

	assert.Contains(t, outputs["logs"], "runtime reload", "the logger was used")
	assert.NotEmpty(t, outputs["details"])

	for name, out := range outputs {
		assert.NotContains(t, out, canaryStamp, "no key in %s", name)
	}
}

// TestReadPathDoesNotAllocate is not parallel on purpose: AllocsPerRun counts the allocations of the parallel
// tests too.
//
//nolint:paralleltest // AllocsPerRun counts allocations from concurrent tests across the process
func TestReadPathDoesNotAllocate(t *testing.T) {
	r := newRig(t, runtime.Hooks{})
	r.start()

	r.feed(content(1, simpleObjs(t, 1)))

	var (
		flagFound bool
		sink      any
	)

	allocs := testing.AllocsPerRun(1000, func() {
		current := r.holder.Load()

		fs, ok := current.FlagSetByKey(keyA)
		if !ok {
			return
		}

		fl, found := fs.Flag("x")
		flagFound, sink = found, fl
	})

	assert.True(t, flagFound)
	assert.NotNil(t, sink)
	assert.InDelta(t, 0, allocs, 0, "Load, FlagSetByKey and Flag allocate nothing")

	_, ok := r.holder.Load().FlagSetByKey("unknown-key")
	assert.False(t, ok)

	var nilSet *runtime.FlagSet

	_, ok = nilSet.Flag("x")
	assert.False(t, ok, "a nil flag set has no flags")
}

func TestZeroHolderLoadsAnEmptyRuntime(t *testing.T) {
	t.Parallel()

	var h runtime.Holder

	current := h.Load()

	require.NotNil(t, current)
	assert.Empty(t, current.FlagSets)
	assert.False(t, current.IsAdminKey("x"))

	_, ok := current.FlagSetByName("x")
	assert.False(t, ok)
}

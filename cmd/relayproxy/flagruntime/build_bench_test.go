//go:build bench

package flagruntime_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/controlplane"
)

const (
	benchSets          = 5000
	benchFlagsPerSet   = 100
	benchSetsPerObject = 10
)

// fleet generates, in memory, benchSets flag sets of benchFlagsPerSet flags. Ten flag sets share one flags
// object, every object has its own content (its own checksum), and a flag set has one API key. variant picks
// the content of the first object, so that two variants give a one-object change.
func fleet(tb testing.TB, variant int) *flagruntime.Content {
	tb.Helper()

	names := make([]string, benchFlagsPerSet)
	for i := range names {
		names[i] = fmt.Sprintf("flag-%03d", i)
	}

	objects := objs{}
	flagSets := make([]controlplane.FlagSet, 0, benchSets)

	for idx := range benchSets / benchSetsPerObject {
		name := fmt.Sprintf("obj-%04d", idx)

		val := idx + 1
		if idx == 0 {
			val = 100000 + variant
		}

		objects[name] = flagsObj(val, names...)

		for s := range benchSetsPerObject {
			set := fmt.Sprintf("set-%04d-%d", idx, s)
			flagSets = append(flagSets, fset(set, src(name), "key-"+set))
		}
	}

	objects[setsName] = setsObj(tb, flagSets...)

	return content(1, objects)
}

// BenchmarkBuildFull is a cold start: every object is parsed and every flag set is built.
func BenchmarkBuildFull(b *testing.B) {
	published := fleet(b, 0)
	svc := newService(b)

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		built, err := flagruntime.NewBuilder(svc, flagruntime.BuilderOptions{}).Build(context.Background(), published)
		require.NoError(b, err)
		require.Equal(b, benchSets, built.Stats.Rebuilt)
	}
}

// BenchmarkBuildOneObjectChange changes one flags object that ten flag sets list.
func BenchmarkBuildOneObjectChange(b *testing.B) {
	variants := [2]*flagruntime.Content{fleet(b, 0), fleet(b, 1)}
	builder := flagruntime.NewBuilder(newService(b), flagruntime.BuilderOptions{})

	built, err := builder.Build(context.Background(), variants[0])
	require.NoError(b, err)
	builder.Commit(built)

	b.ReportAllocs()
	b.ResetTimer()

	i := 0

	for b.Loop() {
		i++

		built, err = builder.Build(context.Background(), variants[i%2])
		if err != nil {
			require.NoError(b, err)
		}

		builder.Commit(built)

		if built.Stats.Rebuilt != benchSetsPerObject || built.Stats.Parsed != 1 {
			require.Equal(b, benchSetsPerObject, built.Stats.Rebuilt)
			require.Equal(b, 1, built.Stats.Parsed)
		}
	}
}

// BenchmarkBuildNoOp is a new generation with the same objects.
func BenchmarkBuildNoOp(b *testing.B) {
	published := fleet(b, 0)
	builder := flagruntime.NewBuilder(newService(b), flagruntime.BuilderOptions{})

	built, err := builder.Build(context.Background(), published)
	require.NoError(b, err)
	builder.Commit(built)

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		built, err = builder.Build(context.Background(), published)
		if err != nil {
			require.NoError(b, err)
		}

		builder.Commit(built)

		if built.Stats.Rebuilt != 0 || built.Stats.Parsed != 0 {
			require.Equal(b, 0, built.Stats.Rebuilt)
			require.Equal(b, 0, built.Stats.Parsed)
		}
	}
}

// engineBench runs an engine over the fleet and returns a function that submits a content and waits for its swap.
func engineBench(b *testing.B, first *flagruntime.Content) func(*flagruntime.Content) {
	b.Helper()

	rounds := make(chan flagruntime.RoundResult, 1)
	eng := flagruntime.NewEngine(nil, newService(b), flagruntime.NewHolder(), flagruntime.WithHooks(flagruntime.Hooks{
		AfterRound: func(res flagruntime.RoundResult) { rounds <- res },
	}))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		defer close(done)

		eng.Run(ctx)
	}()

	go func() {
		for change := range eng.Changes() {
			_ = change // the consumer only drains
		}
	}()

	b.Cleanup(func() {
		cancel()
		<-done
	})

	step := func(content *flagruntime.Content) {
		eng.Submit(content)

		res := <-rounds
		if res.Outcome != flagruntime.OutcomeApplied {
			require.Equal(b, flagruntime.OutcomeApplied, res.Outcome)
		}
	}

	step(first)

	return step
}

// BenchmarkEngineOneObjectChange measures Submit to swap for a change of one flags object.
func BenchmarkEngineOneObjectChange(b *testing.B) {
	variants := [2]*flagruntime.Content{fleet(b, 0), fleet(b, 1)}
	step := engineBench(b, variants[0])

	b.ReportAllocs()
	b.ResetTimer()

	gen := uint64(1)

	for b.Loop() {
		gen++

		step(&flagruntime.Content{Generation: gen, Objects: variants[gen%2].Objects})
	}
}

// BenchmarkEngineNoOp measures Submit to swap for a new generation with the same objects.
func BenchmarkEngineNoOp(b *testing.B) {
	base := fleet(b, 0)
	step := engineBench(b, base)

	b.ReportAllocs()
	b.ResetTimer()

	gen := uint64(1)

	for b.Loop() {
		gen++

		step(&flagruntime.Content{Generation: gen, Objects: base.Objects})
	}
}

// BenchmarkContestedKeys resolves 10000 keys that a new flag set shares with the flag set that runs: the cost is
// linear in the keys.
func BenchmarkContestedKeys(b *testing.B) {
	const keys = 10000

	declared := make([]string, keys)
	for idx := range declared {
		declared[idx] = fmt.Sprintf("key-%05d", idx)
	}

	builder := flagruntime.NewBuilder(newService(b), flagruntime.BuilderOptions{})
	files := objs{"f1": flagsObj(1, "x")}

	built, err := builder.Build(context.Background(), content(1, files.with(objs{
		docs2: setsObj(b, fset("holder", src("f1"), declared...)),
	})))
	require.NoError(b, err)
	builder.Commit(built)

	contested := content(2, files.with(objs{
		docs1: setsObj(b, fset("newcomer", src("f1"), declared...)),
		docs2: setsObj(b, fset("holder", src("f1"), declared...)),
	}))

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		_, err = builder.Build(context.Background(), contested)
		if err != nil {
			require.NoError(b, err)
		}
	}
}

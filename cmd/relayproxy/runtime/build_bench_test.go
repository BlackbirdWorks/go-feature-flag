//go:build bench

package runtime_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/runtime"
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
func fleet(tb testing.TB, variant int) *runtime.Content {
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
		built, err := runtime.NewBuilder(svc, runtime.BuilderOptions{}).Build(context.Background(), published)
		require.NoError(b, err)
		require.Equal(b, benchSets, built.Stats.Rebuilt)
	}
}

// BenchmarkBuildOneObjectChange changes one flags object that ten flag sets list.
func BenchmarkBuildOneObjectChange(b *testing.B) {
	variants := [2]*runtime.Content{fleet(b, 0), fleet(b, 1)}
	builder := runtime.NewBuilder(newService(b), runtime.BuilderOptions{})

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
	builder := runtime.NewBuilder(newService(b), runtime.BuilderOptions{})

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
		}
	}
}

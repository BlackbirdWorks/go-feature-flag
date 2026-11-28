package flagruntime_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/internal/loadtest"
)

// benchContext is a realistic context with a few attributes.
func benchContext() ffcontext.EvaluationContext {
	return ffcontext.NewEvaluationContextBuilder("user-42").
		AddCustom("beta", true).AddCustom("country", "FR").AddCustom("plan", "pro").AddCustom("teamId", "t1").Build()
}

// BenchmarkRoute is the lookup of an API key in a runtime of 2000 flag sets.
func BenchmarkRoute(b *testing.B) {
	sets := make([]flagruntimeSet, 0, 2000)
	for idx := range 2000 {
		sets = append(sets, flagruntimeSet{name: fmt.Sprintf("set-%d", idx), key: fmt.Sprintf("key-%08d", idx)})
	}

	run := runtimeWith(b, bigSets(b, sets))

	for _, bench := range []struct{ name, key string }{
		{name: "hit", key: "key-00001234"},
		{name: "miss", key: "key-nonexistent"},
	} {
		b.Run(bench.name, func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				_, route := run.Route(bench.key)
				require.NotEqual(b, flagruntime.RouteAdmin, route)
			}
		})
	}
}

type flagruntimeSet struct{ name, key string }

func bigSets(tb testing.TB, sets []flagruntimeSet) objs {
	tb.Helper()

	docs := make([]controlplane.FlagSet, 0, len(sets))
	for _, set := range sets {
		docs = append(docs, fset(set.name, src(flagsName), set.key))
	}

	return objs{flagsName: flagsObj(1, "f"), setsName: setsObj(tb, docs...)}
}

// BenchmarkSingleEvaluation compares the version 1 client with the runtime evaluator for the same flag file: the
// allocations of the runtime must not exceed the ones of version 1 for a scalar flag.
//
// The runtime follows the read-only contract of version 1: nothing is copied, so the cost is the one of the engine
// (see also BenchmarkNestedMetadata).
func BenchmarkSingleEvaluation(b *testing.B) {
	data := []byte(benchFlags)
	path := b.TempDir() + "/flags.yaml"
	require.NoError(b, writeFile(path, data))

	legacy := versionOne(b, path, nil, "", nil)
	eval := flagruntime.NewEvaluator(newService(b), nil)
	set := runtimeOf(b, data, "yaml")
	ctx := benchContext()

	for _, flag := range []string{boolStatic, boolRule, objectFlag, "int-flag"} {
		b.Run(flag+"/v1", func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				_, _ = legacy.RawVariation(flag, ctx, nil)
			}
		})

		b.Run(flag+"/runtime", func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				_, _ = eval.RawVariation(b.Context(), set, flag, ctx, nil)
			}
		})
	}

	b.Run("bool-typed/v1", func(b *testing.B) {
		b.ReportAllocs()

		for b.Loop() {
			_, _ = legacy.BoolVariationDetails(boolRule, ctx, false)
		}
	})

	b.Run("bool-typed/runtime", func(b *testing.B) {
		b.ReportAllocs()

		for b.Loop() {
			_, _ = eval.BoolVariationDetails(b.Context(), set, boolRule, ctx, false)
		}
	})
}

// BenchmarkAllFlags compares the evaluation of a flag set of 100 flags.
func BenchmarkAllFlags(b *testing.B) {
	data := []byte(loadtest.ManyFlagsYAML(100))
	path := b.TempDir() + "/flags.yaml"
	require.NoError(b, writeFile(path, data))

	legacy := versionOne(b, path, nil, "", nil)
	eval := flagruntime.NewEvaluator(newService(b), nil)
	set := runtimeOf(b, data, "yaml")
	require.Len(b, set.Flags, 100)

	ctx := benchContext()

	b.Run("v1", func(b *testing.B) {
		b.ReportAllocs()

		for b.Loop() {
			_ = legacy.AllFlagsState(ctx)
		}
	})

	b.Run("runtime", func(b *testing.B) {
		b.ReportAllocs()

		for b.Loop() {
			_ = eval.AllFlagsState(b.Context(), set, ctx)
		}
	})
}

// BenchmarkNestedMetadata is a flag whose metadata holds objects and arrays: version 1 and the runtime both hand out
// the containers of the table, so they cost the same.
func BenchmarkNestedMetadata(b *testing.B) {
	data := readFile(b, shapesFile)
	legacy := versionOne(b, shapesFile, nil, "", nil)
	eval := flagruntime.NewEvaluator(newService(b), nil)
	set := runtimeOf(b, data, "yaml")
	ctx := benchContext()

	b.Run("v1", func(b *testing.B) {
		b.ReportAllocs()

		for b.Loop() {
			_, _ = legacy.RawVariation(boolRule, ctx, nil)
		}
	})

	b.Run("runtime", func(b *testing.B) {
		b.ReportAllocs()

		for b.Loop() {
			_, _ = eval.RawVariation(b.Context(), set, boolRule, ctx, nil)
		}
	})
}

const benchFlags = `
bool-static:
  variations: {on: true, off: false}
  defaultRule: {variation: on}
bool-rule:
  variations: {on: true, off: false}
  targeting:
    - name: beta-users
      query: beta eq true
      variation: on
  defaultRule: {name: everyone, variation: off}
  metadata: {team: payments, tier: gold}
object-flag:
  variations:
    a: {name: alpha, level: 1}
    b: {name: beta, level: 2}
  targeting:
    - name: pro
      query: plan eq "pro"
      variation: b
  defaultRule: {variation: a}
  metadata: {team: payments}
int-flag:
  variations: {low: 1, high: 10}
  defaultRule:
    percentage: {low: 50, high: 50}
`

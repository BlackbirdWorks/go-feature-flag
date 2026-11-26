package flagruntime_test

// The rule engine takes the maps of its query contexts from a sync.Pool. Under the race detector a pool drops items
// at random, so one run of the engine can allocate one map more than another. The tests below therefore take the
// MINIMUM of many single runs (see minAllocs): it is the cost of a run that finds its map in the pool, which is the
// cost without noise, and it is the same figure with and without -race. A regression that adds an allocation to
// every call raises the minimum too; only a one-off allocation (a pool miss) is not seen, and it is not a cost of the
// evaluator.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/internal/flagstate"
	"github.com/blackbirdworks/semaphore/modules/core/evaluation"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
)

// The evaluator adds nothing to the allocations of the engine: each shape is measured through the evaluator and
// through the engine alone (evaluation.Evaluate on the same flag, with the same context), in the same test, and the
// evaluator must not allocate more. A copy of the value or of the metadata would show here at once.
//
// It is not parallel on purpose: AllocsPerRun counts the allocations of the parallel tests too.
//
//nolint:paralleltest // AllocsPerRun counts allocations from concurrent tests across the process
func TestEvaluator_AllocationsEqualTheEngine(t *testing.T) {
	svc := newService(t)
	set := shapesSet(t)
	eval := flagruntime.NewEvaluator(svc, nil)
	ctx := ownedContext()

	tests := []struct {
		name string
		flag string
	}{
		{name: "scalar, no metadata", flag: "bool-static"},
		{name: "scalar, flat metadata", flag: "bool-flat-meta"},
		{name: "scalar, nested metadata", flag: boolRule},
		{name: "object value", flag: objectFlag},
		{name: "array value", flag: "array-flag"},
		{name: "scheduled object value", flag: "scheduled-object-flag"},
	}

	for _, tt := range tests {
		flag, ok := set.Flag(tt.flag)
		if !assert.True(t, ok, tt.flag) {
			continue
		}

		engine := minAllocs(func() {
			_, _ = evaluation.Evaluate[any](flag, tt.flag, ctx, flagdef.Context{Service: svc}, "interface{}", nil)
		})
		raw := minAllocs(func() { _, _ = eval.RawVariation(set, tt.flag, ctx, nil) })

		assert.InDelta(t, engine, raw, 0, "RawVariation of %s: engine %v, evaluator %v", tt.name, engine, raw)
		t.Logf("%-28s engine alone %v allocs, RawVariation %v", tt.name, engine, raw)
	}
}

// A typed object or array call costs the engine plus one copy of the value, and never two: the copy of a scheduled
// flag is the engine's own, so there the typed call costs what RawVariation costs.
//
//nolint:paralleltest // AllocsPerRun counts allocations from concurrent tests across the process
func TestEvaluator_TypedJSONCopiesOnce(t *testing.T) {
	svc := newService(t)
	set := shapesSet(t)
	eval := flagruntime.NewEvaluator(svc, nil)
	ctx := ownedContext()

	tests := []struct {
		typed  func() any
		engine func(*flagdef.InternalFlag) any
		name   string
		flag   string
		copies bool
	}{
		{
			name: "object", flag: objectFlag, copies: true,
			typed: func() any {
				res, _ := eval.JSONVariationDetails(set, objectFlag, ctx, nil)

				return res.Value
			},
			engine: func(flag *flagdef.InternalFlag) any {
				res, _ := evaluation.Evaluate[map[string]any](
					flag, objectFlag, ctx, flagdef.Context{Service: svc}, "map[string]interface{}", nil)

				return res.Value
			},
		},
		{
			name:   "array",
			flag:   "array-flag",
			copies: true,
			typed: func() any {
				res, _ := eval.JSONArrayVariationDetails(set, "array-flag", ctx, nil)

				return res.Value
			},
			engine: func(flag *flagdef.InternalFlag) any {
				res, _ := evaluation.Evaluate[[]any](
					flag, "array-flag", ctx, flagdef.Context{Service: svc}, "[]interface{}", nil)

				return res.Value
			},
		},
		{
			name: "scheduled object", flag: "scheduled-object-flag", copies: false,
			typed: func() any {
				res, _ := eval.JSONVariationDetails(set, "scheduled-object-flag", ctx, nil)

				return res.Value
			},
			engine: func(flag *flagdef.InternalFlag) any {
				res, _ := evaluation.Evaluate[map[string]any](
					flag, "scheduled-object-flag", ctx, flagdef.Context{Service: svc}, "map[string]interface{}", nil)

				return res.Value
			},
		},
	}

	for _, tt := range tests {
		flag, _ := set.Flag(tt.flag)
		engine := minAllocs(func() { _ = tt.engine(flag) })
		typed := minAllocs(func() { _ = tt.typed() })

		// the copy as the typed call makes it: from the typed value (boxing a slice header is an allocation)
		held := tt.engine(flag)
		copyCost := minAllocs(func() { _ = cloneTyped(held) })

		want := engine
		if tt.copies {
			want = engine + copyCost
		}

		assert.InDelta(t, want, typed, 0, "%s: engine %v, copy %v, typed %v", tt.name, engine, copyCost, typed)
		t.Logf("%-18s engine alone (typed) %v allocs, one copy %v, typed call %v", tt.name, engine, copyCost, typed)
	}
}

// Evaluating the 100 flags of a flag set costs the engine alone (the evaluation of every flag and the table of
// states): the evaluator adds nothing, and the all-flags state does not copy the values.
//
//nolint:paralleltest // AllocsPerRun counts allocations from concurrent tests across the process
func TestEvaluator_AllFlagsAllocationsEqualTheEngine(t *testing.T) {
	svc := newService(t)
	data := staticFlagsYAML(100)
	set := runtimeOf(t, data, "yaml")
	eval := flagruntime.NewEvaluator(svc, nil)
	ctx := ffcontext.NewEvaluationContext("user-42")

	flagCtx := flagdef.Context{Service: svc}
	engine := minAllocs(func() { _ = engineAllFlags(set.Flags, ctx, flagCtx) })
	got := minAllocs(func() { _ = eval.AllFlagsState(set, ctx) })

	assert.Len(t, set.Flags, 100)
	assert.InDelta(t, engine, got, 0, "engine alone %v, evaluator %v", engine, got)
	t.Logf("100 flags: engine alone %v allocs, AllFlagsState %v", engine, got)
}

// engineAllFlags is what the engine alone does for the state of all the flags: one evaluation per flag, and the table.
func engineAllFlags(
	flags map[string]*flagdef.InternalFlag, ctx ffcontext.Context, flagCtx flagdef.Context,
) flagstate.AllFlags {
	states := flagstate.NewAllFlags()

	for key, flag := range flags {
		state := flagstate.FromFlagEvaluation(key, ctx, flagCtx, flag)
		states.AddFlag(key, &state)
	}

	return states
}

// cloneTyped copies a typed value the way the typed calls do: through flagdef.CloneValue, from the typed variable.
func cloneTyped(val any) any {
	switch typed := val.(type) {
	case []any:
		return flagdef.CloneValue(typed)
	case map[string]any:
		return flagdef.CloneValue(typed)
	default:
		return val
	}
}

// minAllocs is the least number of allocations of one run of the function over many runs. Each run is measured
// alone, after a warm-up run, so a run that misses the [sync.Pool] of the rule engine (always possible under the
// race detector, and after a garbage collection) does not move the figure.
func minAllocs(run func()) float64 {
	const runs = 60

	best := testing.AllocsPerRun(1, run)

	for range runs - 1 {
		best = min(best, testing.AllocsPerRun(1, run))
	}

	return best
}

// staticFlagsYAML is n flags without a query (static and percentage ones): they do not use the pool of the rule
// engine, so the cost of the whole table does not move between runs.
func staticFlagsYAML(count int) []byte {
	var out strings.Builder

	for idx := range count {
		if idx%2 == 0 {
			fmt.Fprintf(&out, "flag-%d:\n  variations: {A: true, B: false}\n  defaultRule: {variation: A}\n", idx)

			continue
		}

		fmt.Fprintf(
			&out,
			"flag-%d:\n  variations: {A: true, B: false}\n  defaultRule: {percentage: {A: 50, B: 50}}\n",
			idx,
		)
	}

	return []byte(out.String())
}

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
	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/internal/flagstate"
	"github.com/blackbirdworks/semaphore/modules/core/evaluation"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
	"github.com/blackbirdworks/semaphore/modules/core/model"
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

	// every row says what its name claims, and the claim is asserted on the engine's result: the kind of the value, the
	// exact metadata (none, flat or with nested containers), the reason and the scheduled steps
	tests := []struct {
		wantMetadata  map[string]any
		name          string
		flag          string
		wantReason    string
		wantValueKind string
		wantScheduled bool
	}{
		{name: "scalar, no metadata", flag: boolStatic, wantReason: flagdef.ReasonStatic, wantValueKind: kindBool},
		{
			name: "scalar, flat metadata", flag: "bool-flat-meta", wantReason: flagdef.ReasonTargetingMatch,
			wantValueKind: kindBool,
			wantMetadata:  map[string]any{ruleNameKey: "beta-users", "team": "payments", "tier": "gold"},
		},
		{
			name: "scalar, nested metadata", flag: boolRule, wantReason: flagdef.ReasonTargetingMatch,
			wantValueKind: kindBool,
			wantMetadata: map[string]any{
				ruleNameKey: "beta-users", "team": "payments", listKey: []any{1, 2, map[string]any{"x": 1}},
				"owner": map[string]any{nameKey: "ops", tagsKey: []any{"a", "b"}},
			},
		},
		{
			name: "object value", flag: objectFlag, wantReason: flagdef.ReasonTargetingMatch, wantValueKind: kindMap,
			wantMetadata: map[string]any{ruleNameKey: "pro-rule", "kind": map[string]any{"level": 1}},
		},
		{name: "array value", flag: arrayFlag, wantReason: flagdef.ReasonSplit, wantValueKind: kindList},
		{
			name: "scheduled object value", flag: scheduledObject, wantReason: flagdef.ReasonStatic,
			wantValueKind: kindMap, wantScheduled: true,
		},
	}

	for _, tt := range tests {
		flag, ok := set.Flag(tt.flag)
		if !assert.True(t, ok, tt.flag) {
			continue
		}

		// what each side returned is kept and compared after the measurement: the work cannot be dropped, and both
		// sides must answer the same
		var (
			engineRes model.VariationResult[any]
			rawRes    model.RawVarResult
		)

		engine := minAllocs(func() {
			engineRes, _ = evaluation.Evaluate[any](
				flag,
				tt.flag,
				ctx,
				flagdef.Context{Service: svc},
				"interface{}",
				nil,
			)
		})
		raw := minAllocs(func() { rawRes, _ = eval.RawVariation(set, tt.flag, ctx, nil) })

		assert.Equal(t, tt.wantReason, rawRes.Reason, tt.name)
		assert.Equal(t, tt.wantValueKind, valueKind(rawRes.Value), "%s: the kind of the value", tt.name)
		assert.Equal(t, tt.wantMetadata, engineRes.Metadata, "%s: the metadata of the engine", tt.name)
		assert.Equal(t, tt.wantMetadata, rawRes.Metadata, "%s: the metadata of the evaluator", tt.name)
		assert.Equal(t, tt.wantScheduled, flag.Scheduled != nil, tt.name)
		assert.NotEmpty(t, rawRes.VariationType, tt.name)
		assert.Equal(t, engineRes.Value, rawRes.Value, tt.name)
		assert.Equal(t, engineRes.Metadata, rawRes.Metadata, tt.name)
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
		typed         func() any
		engine        func(*flagdef.InternalFlag) any
		name          string
		flag          string
		wantValueKind string
		copies        bool
		wantScheduled bool
	}{
		{
			name: kindObject, flag: objectFlag, copies: true, wantValueKind: kindMap,
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
			name:          "array",
			flag:          arrayFlag,
			copies:        true,
			wantValueKind: kindList,
			typed: func() any {
				res, _ := eval.JSONArrayVariationDetails(set, arrayFlag, ctx, nil)

				return res.Value
			},
			engine: func(flag *flagdef.InternalFlag) any {
				res, _ := evaluation.Evaluate[[]any](
					flag, arrayFlag, ctx, flagdef.Context{Service: svc}, "[]interface{}", nil)

				return res.Value
			},
		},
		{
			name: "scheduled object", flag: scheduledObject, copies: false, wantScheduled: true,
			wantValueKind: kindMap,
			typed: func() any {
				res, _ := eval.JSONVariationDetails(set, scheduledObject, ctx, nil)

				return res.Value
			},
			engine: func(flag *flagdef.InternalFlag) any {
				res, _ := evaluation.Evaluate[map[string]any](
					flag, scheduledObject, ctx, flagdef.Context{Service: svc}, "map[string]interface{}", nil)

				return res.Value
			},
		},
	}

	for _, tt := range tests {
		flag, _ := set.Flag(tt.flag)

		assert.Equal(t, tt.wantScheduled, flag.Scheduled != nil, "%s: the path of the flag", tt.name)
		assert.Equal(t, tt.wantValueKind, valueKind(tt.engine(flag)), "%s: the kind of the value", tt.name)
		assert.Equal(
			t,
			!tt.wantScheduled,
			tt.copies,
			"%s: only a flag without scheduled steps is copied by the call",
			tt.name,
		)

		var engineValue, typedValue, copied any

		engine := minAllocs(func() { engineValue = tt.engine(flag) })
		typed := minAllocs(func() { typedValue = tt.typed() })

		// the copy as the typed call makes it: from the typed value (boxing a slice header is an allocation)
		held := tt.engine(flag)
		copyCost := minAllocs(func() { copied = cloneTyped(held) })

		assert.Equal(t, engineValue, typedValue, tt.name)
		assert.Equal(t, held, copied, tt.name)

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

	var engineStates, gotStates flagstate.AllFlags

	engine := minAllocs(func() { engineStates = engineAllFlags(set.Flags, ctx, flagCtx) })
	got := minAllocs(func() { gotStates = eval.AllFlagsState(set, ctx) })

	assert.Len(t, set.Flags, 100)

	// the flags are what the comment says: no rule and no query, so the pool of the rule engine is never used
	for key, flag := range set.Flags {
		assert.Empty(t, flag.Rules, "%s has no targeting rule", key)
	}

	assert.Equal(t, engineStates.GetFlags(), gotStates.GetFlags())
	assert.Len(t, gotStates.GetFlags(), 100)
	assert.InDelta(t, engine, got, 0, "engine alone %v, evaluator %v", engine, got)
	t.Logf("100 flags: engine alone %v allocs, AllFlagsState %v", engine, got)
}

// With a hook that takes the events, a tracked evaluation costs what it costs without the hook plus the event, and
// plus one snapshot of the value when the caller keeps the value it gets back: the rows are the scalar flag, the
// object value of the raw call and the typed object (the event holds the flag's value, the caller has a copy that the
// figure without the hook already counts), then a typed scheduled flag (the engine's copy goes to the caller, the event
// gets a snapshot) and a missing flag with a default of the caller (the default goes back as given, the event gets
// a snapshot). The event is built by the exporter package with the same inputs.
//
//nolint:paralleltest // AllocsPerRun counts allocations from concurrent tests across the process
func TestEvaluator_TrackedAllocations(t *testing.T) {
	svc := newService(t)
	set := shapesSet(t)
	ctx := ownedContext()
	plain := flagruntime.NewEvaluator(svc, nil)

	var kept *exporter.FeatureEvent

	tracking := flagruntime.NewEvaluator(svc, &flagruntime.EvaluatorConfig{
		TrackEvent: func(event *exporter.FeatureEvent) { kept = event },
	})

	newDefault := func() map[string]any { return map[string]any{"d": []any{1}} }

	tests := []struct {
		run           func(eval *flagruntime.Evaluator) any
		def           map[string]any
		name          string
		flag          string
		wantReason    string
		wantErrorCode string
		wantValueKind string
		snapshot      bool
		wantScheduled bool
	}{
		{
			name: "scalar flag", flag: trackedFlag, wantReason: flagdef.ReasonStatic, wantValueKind: kindBool,
			run: func(eval *flagruntime.Evaluator) any {
				res, _ := eval.RawVariation(set, trackedFlag, ctx, nil)

				return res.Value
			},
		},
		{
			name:          "object value, raw",
			flag:          objectFlag,
			wantReason:    flagdef.ReasonTargetingMatch,
			wantValueKind: kindMap,
			run: func(eval *flagruntime.Evaluator) any {
				res, _ := eval.RawVariation(set, objectFlag, ctx, nil)

				return res.Value
			},
		},
		{
			name: "typed object", flag: objectFlag, wantReason: flagdef.ReasonTargetingMatch, wantValueKind: kindMap,
			run: func(eval *flagruntime.Evaluator) any {
				res, _ := eval.JSONVariationDetails(set, objectFlag, ctx, nil)

				return res.Value
			},
		},
		{
			name: "typed scheduled object", flag: "scheduled-tracked-object-flag", snapshot: true,
			wantReason: flagdef.ReasonStatic, wantScheduled: true, wantValueKind: kindMap,
			run: func(eval *flagruntime.Evaluator) any {
				res, _ := eval.JSONVariationDetails(set, "scheduled-tracked-object-flag", ctx, nil)

				return res.Value
			},
		},
		{
			name: "missing flag, typed with a default", flag: missingKey, snapshot: true, def: newDefault(),
			wantReason: flagdef.ReasonError, wantErrorCode: flagdef.ErrorCodeFlagNotFound, wantValueKind: kindMap,
			run: func(eval *flagruntime.Evaluator) any {
				res, _ := eval.JSONVariationDetails(set, missingKey, ctx, newDefault())

				return res.Value
			},
		},
	}

	for _, tt := range tests {
		res, _ := plain.RawVariation(set, tt.flag, ctx, tt.def)

		assert.Equal(t, tt.wantReason, res.Reason, tt.name)
		assert.Equal(t, tt.wantErrorCode, res.ErrorCode, tt.name)
		assert.True(t, res.TrackEvents, "%s: the evaluation is tracked", tt.name)
		assert.Equal(t, tt.wantValueKind, valueKind(res.Value), "%s: the kind of the value", tt.name)

		if flag, found := set.Flag(tt.flag); found {
			assert.Equal(t, tt.wantScheduled, flag.Scheduled != nil, "%s: the path of the flag", tt.name)
		}

		value, evalCtx := res.Value, ffcontext.Context(ctx)

		var built *exporter.FeatureEvent

		eventCost := minAllocs(func() {
			built = exporter.NewFeatureEvent(
				evalCtx, tt.flag, value, res.VariationType, res.Failed, res.Version, "SERVER",
				evalCtx.ExtractGOFFProtectedFields().ExporterMetadata)
		})

		assert.Equal(t, tt.flag, built.Key, tt.name)

		copyCost := 0.0

		if tt.snapshot {
			var snapshot any

			copyCost = minAllocs(func() { snapshot = cloneTyped(value) })

			assert.Equal(t, value, snapshot, tt.name)
		}

		var plainValue, trackedValue any

		kept = nil

		without := minAllocs(func() { plainValue = tt.run(plain) })
		with := minAllocs(func() { trackedValue = tt.run(tracking) })

		assert.Equal(t, plainValue, trackedValue, tt.name)
		assert.NotNil(t, kept, tt.name)

		assert.InDelta(
			t,
			without+eventCost+copyCost,
			with,
			0,
			"%s: without the hook %v, event %v, snapshot %v, with the hook %v",
			tt.name,
			without,
			eventCost,
			copyCost,
			with,
		)
		t.Logf(
			"%-36s without the hook %v, event %v, snapshot %v, with the hook %v",
			tt.name,
			without,
			eventCost,
			copyCost,
			with,
		)
	}
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

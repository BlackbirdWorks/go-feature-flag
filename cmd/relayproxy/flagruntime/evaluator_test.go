package flagruntime_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/handler/ofrep"
	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
	"github.com/blackbirdworks/semaphore/modules/core/model"
)

func shapesSet(tb testing.TB) *flagruntime.FlagSet {
	tb.Helper()

	return runtimeOf(tb, readFile(tb, shapesFile), "yaml")
}

// Mutating what an evaluation returned never changes the table: the next evaluation, the configuration view and
// the hash are those of the first one. Every owner of a copy is changed: the value (map or slice, nested), the
// metadata (nested containers too), the states of all flags.
func TestAliasing_ReturnedValuesAreCopies(t *testing.T) {
	t.Parallel()

	set := shapesSet(t)
	eval := flagruntime.NewEvaluator(newService(t), nil)
	ctx := ffcontext.NewEvaluationContextBuilder("user-2").
		AddCustom("beta", true).AddCustom(planAttr, proPlan).AddCustom("country", "FR").Build()
	before, changeErr := set.Change()
	require.NoError(t, changeErr)

	tests := []struct {
		mutate func(t *testing.T)
		// again evaluates again and returns what must be unchanged
		again func() any
		name  string
	}{
		{
			name: "raw object value, nested",
			mutate: func(t *testing.T) {
				t.Helper()

				res, _ := eval.RawVariation(set, "object-flag", ctx, nil)
				obj, ok := res.Value.(map[string]any)
				require.True(t, ok)

				obj["name"] = mutated
				obj["added"] = 1
				nested, ok := obj["nested"].(map[string]any)
				require.True(t, ok)

				nested["deep"] = []any{mutated}
			},
			again: func() any {
				res, _ := eval.RawVariation(set, "object-flag", ctx, nil)

				return res.Value
			},
		},
		{
			name: "raw array value, nested",
			mutate: func(t *testing.T) {
				t.Helper()

				res, _ := eval.RawVariation(set, "array-flag", ctx, nil)
				arr, ok := res.Value.([]any)
				require.True(t, ok)

				arr[0] = mutated

				if inner, isArr := arr[len(arr)-1].([]any); isArr {
					inner[0] = mutated
				}
			},
			again: func() any {
				res, _ := eval.RawVariation(set, "array-flag", ctx, nil)

				return res.Value
			},
		},
		{
			name: "typed object and array",
			mutate: func(t *testing.T) {
				t.Helper()

				obj, _ := eval.JSONVariationDetails(set, "object-flag", ctx, nil)
				obj.Value["name"] = mutated

				arr, _ := eval.JSONArrayVariationDetails(set, "array-flag", ctx, nil)
				arr.Value[0] = mutated
			},
			again: func() any {
				obj, _ := eval.JSONVariationDetails(set, "object-flag", ctx, nil)
				arr, _ := eval.JSONArrayVariationDetails(set, "array-flag", ctx, nil)

				return []any{obj.Value, arr.Value}
			},
		},
		{
			name: "metadata, nested containers",
			mutate: func(t *testing.T) {
				t.Helper()

				res, _ := eval.RawVariation(set, "bool-rule", ctx, nil)
				res.Metadata["team"] = mutated
				res.Metadata["evaluatedRuleName"] = mutated

				owner, ok := res.Metadata["owner"].(map[string]any)
				require.True(t, ok)

				owner["name"] = mutated

				tags, ok := owner["tags"].([]any)
				require.True(t, ok)

				tags[0] = mutated

				list, ok := res.Metadata["list"].([]any)
				require.True(t, ok)

				list[0] = mutated

				inner, ok := list[2].(map[string]any)
				require.True(t, ok)

				inner["x"] = mutated
			},
			again: func() any {
				res, _ := eval.RawVariation(set, "bool-rule", ctx, nil)

				return res.Metadata
			},
		},
		{
			name: "metadata of a disabled flag",
			mutate: func(t *testing.T) {
				t.Helper()

				res, _ := eval.RawVariation(set, "bool-disabled", ctx, nil)
				res.Metadata["reason"] = mutated
			},
			again: func() any {
				res, _ := eval.RawVariation(set, "bool-disabled", ctx, nil)

				return res.Metadata
			},
		},
		{
			name: "scheduled object value",
			mutate: func(t *testing.T) {
				t.Helper()

				res, _ := eval.RawVariation(set, "scheduled-object-flag", ctx, nil)
				obj, ok := res.Value.(map[string]any)
				require.True(t, ok)

				obj["k"] = mutated
			},
			again: func() any {
				res, _ := eval.RawVariation(set, "scheduled-object-flag", ctx, nil)

				return res.Value
			},
		},
		{
			name: "all flags state values and metadata",
			mutate: func(t *testing.T) {
				t.Helper()

				states := eval.AllFlagsState(set, ctx)
				for _, state := range states.GetFlags() {
					if obj, ok := state.Value.(map[string]any); ok {
						obj["name"] = mutated
					}

					if arr, ok := state.Value.([]any); ok && len(arr) > 0 {
						arr[0] = mutated
					}

					for key := range state.Metadata {
						state.Metadata[key] = mutated
					}
				}
			},
			again: func() any { return mustJSON(t, eval.AllFlagsState(set, ctx)) },
		},
		{
			name: "configuration view",
			mutate: func(t *testing.T) {
				t.Helper()

				for _, flag := range set.Configuration(nil) {
					internal, ok := flag.(*flagdef.InternalFlag)
					require.True(t, ok)

					for key := range internal.GetVariations() {
						delete(internal.GetVariations(), key)
					}

					internal.Metadata = nil
				}
			},
			again: func() any { return mustJSON(t, set.Configuration(nil)) },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			first := mustJSON(t, tt.again())

			tt.mutate(t)

			assert.Equal(t, first, mustJSON(t, tt.again()))

			after, err := set.Change()
			require.NoError(t, err)
			assert.Equal(t, before, after, "the table did not change")
		})
	}
}

// The SDK default of the caller is returned as given: the evaluator does not copy what the caller owns.
func TestEvaluator_DefaultValueIsReturnedAsGiven(t *testing.T) {
	t.Parallel()

	set := shapesSet(t)
	eval := flagruntime.NewEvaluator(newService(t), nil)
	dflt := map[string]any{"d": 1}

	res, err := eval.RawVariation(set, missingKey, ffcontext.NewEvaluationContext("u"), dflt)
	require.ErrorIs(t, err, flagruntime.ErrFlagNotPresent)

	got, ok := res.Value.(map[string]any)
	require.True(t, ok)

	got["d"] = 2

	assert.Equal(t, 2, dflt["d"])
	assert.Equal(t, flagdef.ErrorCodeFlagNotFound, res.ErrorCode)
}

// The hook gets the event of a tracked evaluation, and only then: nothing for a flag that does not track, nothing
// without a hook (the evaluation works), and the event describes the enriched context.
func TestEvaluator_TrackEventHook(t *testing.T) {
	t.Parallel()

	set := shapesSet(t)
	ctx := ffcontext.NewEvaluationContext("user-1")

	tests := []struct {
		enrichment map[string]any
		name       string
		flag       string
		wantKind   string
		wantEvents int
		withHook   bool
	}{
		{name: "tracked flag", flag: trackedFlag, withHook: true, wantEvents: 1, wantKind: "user"},
		{name: "untracked flag", flag: "bool-untracked", withHook: true, wantEvents: 0},
		{name: "no hook", flag: trackedFlag, withHook: false, wantEvents: 0},
		{
			name: "anonymous by enrichment", flag: trackedFlag, withHook: true, wantEvents: 1,
			enrichment: map[string]any{"anonymous": true}, wantKind: "anonymousUser",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var events []*exporter.FeatureEvent

			cfg := &flagruntime.EvaluatorConfig{EvaluationContextEnrichment: tt.enrichment}
			if tt.withHook {
				cfg.TrackEvent = func(event *exporter.FeatureEvent) { events = append(events, event) }
			}

			eval := flagruntime.NewEvaluator(newService(t), cfg)

			res, err := eval.RawVariation(set, tt.flag, ctx, nil)
			require.NoError(t, err)
			assert.Equal(t, tt.flag == trackedFlag, res.TrackEvents)

			require.Len(t, events, tt.wantEvents)

			if tt.wantEvents > 0 {
				assert.Equal(t, tt.flag, events[0].Key)
				assert.Equal(t, "SERVER", events[0].Source)
				assert.Equal(t, tt.wantKind, events[0].ContextKind)
			}
		})
	}
}

// A nil flag set (not available) answers like an empty one; nil configuration is the zero configuration.
func TestEvaluator_NilFlagSet(t *testing.T) {
	t.Parallel()

	eval := flagruntime.NewEvaluator(newService(t), nil)
	ctx := ffcontext.NewEvaluationContext("u")

	res, err := eval.RawVariation(nil, "x", ctx, "dflt")
	require.ErrorIs(t, err, flagruntime.ErrFlagNotPresent)
	assert.Equal(t, flagdef.ErrorCodeFlagNotFound, res.ErrorCode)
	assert.Equal(t, "dflt", res.Value)

	all := eval.AllFlagsState(nil, ctx)
	assert.True(t, all.IsValid())
	assert.Empty(t, all.GetFlags())
	assert.Equal(t, `{"valid":true}`, mustJSON(t, all))

	listed := eval.GetFlagStates(nil, ctx, []string{"x"})
	assert.Empty(t, listed.GetFlags())

	var set *flagruntime.FlagSet

	assert.Empty(t, set.Configuration(nil))

	change, err := set.Change()
	require.NoError(t, err)
	assert.Equal(t, flagruntime.FlagChange{Hash: change.Hash, Flags: map[string]uint32{}}, change)
	assert.Empty(t, eval.EvaluationContextEnrichment(nil))
}

// A flag set with its own enrichment evaluates with it, the others with the default one.
func TestEvaluator_PerFlagSetEnrichment(t *testing.T) {
	t.Parallel()

	data := []byte("pro-only:\n  variations:\n    on: true\n    off: false\n  targeting:\n" +
		"    - query: plan eq \"pro\"\n      variation: on\n  defaultRule:\n    variation: off\n")
	run := runtimeWith(t, objs{
		flagsName: obj(flagruntime.KindFlags, data),
		setsName: setsObj(t,
			fset(proPlan, src(flagsName), "key-pro"),
			fset(freePlan, src(flagsName), "key-free"),
			fset("plain", src(flagsName), "key-plain")),
	})
	eval := flagruntime.NewEvaluator(newService(t), &flagruntime.EvaluatorConfig{
		EvaluationContextEnrichment: map[string]any{planAttr: freePlan},
		FlagSetEnrichment:           map[string]map[string]any{proPlan: {planAttr: proPlan}},
		Environment:                 "prod",
	})
	ctx := ffcontext.NewEvaluationContext("u")

	tests := []struct {
		wantValue      any
		wantEnrichment map[string]any
		flagSet        string
	}{
		{flagSet: proPlan, wantValue: true, wantEnrichment: map[string]any{planAttr: proPlan}},
		{flagSet: freePlan, wantValue: false, wantEnrichment: map[string]any{planAttr: freePlan}},
		{flagSet: "plain", wantValue: false, wantEnrichment: map[string]any{planAttr: freePlan}},
	}

	for _, tt := range tests {
		t.Run(tt.flagSet, func(t *testing.T) {
			t.Parallel()

			set := run.FlagSets[tt.flagSet]
			res, err := eval.RawVariation(set, "pro-only", ctx, nil)
			require.NoError(t, err)
			assert.Equal(t, tt.wantValue, res.Value)
			assert.Equal(t, tt.wantEnrichment, eval.EvaluationContextEnrichment(set))
		})
	}
}

// The enrichment the caller passes is not shared with the evaluator, and the one it returns is a copy.
func TestEvaluator_EnrichmentIsCopied(t *testing.T) {
	t.Parallel()

	in := map[string]any{planAttr: proPlan}
	eval := flagruntime.NewEvaluator(newService(t), &flagruntime.EvaluatorConfig{EvaluationContextEnrichment: in})

	in["plan"] = mutated

	out := eval.EvaluationContextEnrichment(nil)
	assert.Equal(t, map[string]any{planAttr: proPlan}, out)

	out["plan"] = mutated

	assert.Equal(t, map[string]any{planAttr: proPlan}, eval.EvaluationContextEnrichment(nil))
}

// The response metadata is the one the version 1 OFREP handler writes: the cacheable keys only for a cacheable
// result, the Semaphore one only when it is not disabled, and the metadata of the result is the map that is extended.
func TestEvaluator_ResponseMetadata(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantKeys map[string]any
		in       model.RawVarResult
		name     string
		disable  bool
	}{
		{name: "not cacheable, no metadata", in: model.RawVarResult{}, wantKeys: nil},
		{
			name: "not cacheable keeps its metadata", in: model.RawVarResult{Metadata: map[string]any{"a": 1}},
			wantKeys: map[string]any{"a": 1},
		},
		{
			name: "cacheable without metadata", in: model.RawVarResult{Cacheable: true},
			wantKeys: map[string]any{flagruntime.MetadataCacheable: true, flagruntime.LegacyMetadataCacheable: true},
		},
		{
			name: "cacheable with metadata",
			in:   model.RawVarResult{Cacheable: true, Metadata: map[string]any{"a": 1}},
			wantKeys: map[string]any{
				"a":                                 1,
				flagruntime.MetadataCacheable:       true,
				flagruntime.LegacyMetadataCacheable: true,
			},
		},
		{
			name: "cacheable, Semaphore key disabled", in: model.RawVarResult{Cacheable: true}, disable: true,
			wantKeys: map[string]any{flagruntime.LegacyMetadataCacheable: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			eval := flagruntime.NewEvaluator(
				newService(t),
				&flagruntime.EvaluatorConfig{DisableSemaphoreMetadata: tt.disable},
			)

			assert.Equal(t, tt.wantKeys, eval.ResponseMetadata(&tt.in))
		})
	}

	assert.Equal(t, ofrep.MetadataCacheable, flagruntime.MetadataCacheable)
	assert.Equal(t, ofrep.LegacyMetadataCacheable, flagruntime.LegacyMetadataCacheable)
}

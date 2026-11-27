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
		// wantFlagTracks is the trackEvents setting of the flag of the row, asserted on the result
		wantFlagTracks bool
	}{
		{
			name:           "tracked flag",
			flag:           trackedFlag,
			withHook:       true,
			wantEvents:     1,
			wantKind:       "user",
			wantFlagTracks: true,
		},
		{name: "untracked flag", flag: "bool-untracked", withHook: true, wantEvents: 0, wantFlagTracks: false},
		{name: "no hook", flag: trackedFlag, withHook: false, wantEvents: 0, wantFlagTracks: true},
		{
			name: "anonymous by enrichment", flag: trackedFlag, withHook: true, wantEvents: 1, wantFlagTracks: true,
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
			assert.Equal(t, tt.wantFlagTracks, res.TrackEvents, "the trackEvents setting of the flag")

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
		Environment:                 prodEnv,
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

// The enrichment the caller passes is copied deeply, for the default and for a flag set that has its own: changing
// the input of either, or the output of either, at the top level or in a nested map or slice, changes nothing: not
// the other input's copy, not either output, not the evaluations. Each mutation runs against every target, and each
// row asserts that its change took effect on its target, so none is vacuous. A shallow copy of any of the four fails.
func TestEvaluator_EnrichmentIsCopied(t *testing.T) {
	t.Parallel()

	targets := []struct {
		pick func(defIn, namedIn, defOut, namedOut map[string]any) map[string]any
		name string
	}{
		{name: "default input", pick: func(defIn, _, _, _ map[string]any) map[string]any { return defIn }},
		{name: "named input", pick: func(_, namedIn, _, _ map[string]any) map[string]any { return namedIn }},
		{name: "default output", pick: func(_, _, defOut, _ map[string]any) map[string]any { return defOut }},
		{name: "named output", pick: func(_, _, _, namedOut map[string]any) map[string]any { return namedOut }},
	}

	mutations := []struct {
		change func(target map[string]any)
		name   string
	}{
		{name: "top level key", change: func(target map[string]any) { target[planAttr] = mutated }},
		{
			name: "nested map",
			change: func(target map[string]any) {
				nested, _ := target[orgKey].(map[string]any)
				nested[nameKey] = mutated
			},
		},
		{
			name: "nested slice",
			change: func(target map[string]any) {
				tags, _ := target[tagsKey].([]any)
				tags[0] = mutated
			},
		},
	}

	newEnrichment := func() map[string]any {
		return map[string]any{planAttr: proPlan, orgKey: map[string]any{nameKey: acmeOrg}, tagsKey: []any{"a", "b"}}
	}

	// two flag sets that use one flag, which is on only when the enrichment says the organisation is acme
	data := []byte("org-flag:\n  variations:\n    on: true\n    off: false\n  targeting:\n" +
		"    - query: org.name eq \"acme\"\n      variation: on\n  defaultRule:\n    variation: off\n")
	run := runtimeWith(t, objs{
		flagsName: obj(flagruntime.KindFlags, data),
		setsName:  setsObj(t, fset(namedSet, src(flagsName), "key-named"), fset("other", src(flagsName), "key-other")),
	})

	for _, target := range targets {
		for _, mutation := range mutations {
			t.Run(target.name+"/"+mutation.name, func(t *testing.T) {
				t.Parallel()

				defIn, namedIn := newEnrichment(), newEnrichment()
				eval := flagruntime.NewEvaluator(newService(t), &flagruntime.EvaluatorConfig{
					EvaluationContextEnrichment: defIn,
					FlagSetEnrichment:           map[string]map[string]any{namedSet: namedIn},
					Environment:                 prodEnv,
				})

				named, other := run.FlagSets[namedSet], run.FlagSets["other"]
				defOut, namedOut := eval.EvaluationContextEnrichment(other), eval.EvaluationContextEnrichment(named)

				picked := target.pick(defIn, namedIn, defOut, namedOut)
				mutation.change(picked)

				// the change took effect on the target of the row (the row is not vacuous)
				assert.NotEqual(t, newEnrichment(), picked, "the target was changed")

				// and reached nothing else: both outputs are fresh copies of the original, and so are the evaluations
				assert.Equal(t, newEnrichment(), eval.EvaluationContextEnrichment(other), "the default output")
				assert.Equal(t, newEnrichment(), eval.EvaluationContextEnrichment(named), "the named output")

				for _, set := range []*flagruntime.FlagSet{named, other} {
					res, err := eval.RawVariation(set, "org-flag", ffcontext.NewEvaluationContext("u"), nil)
					require.NoError(t, err)
					assert.Equal(t, true, res.Value, "the evaluation of %s", set.Name)
				}
			})
		}
	}
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

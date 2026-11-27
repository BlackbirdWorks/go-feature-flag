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

// The enrichment the caller passes is copied deeply: changing the input (nested containers too) after NewEvaluator
// changes nothing, and so does changing what the evaluator returns. Each case would fail with a shallow copy.
func TestEvaluator_EnrichmentIsCopied(t *testing.T) {
	t.Parallel()

	tests := []struct {
		change func(in map[string]any, out map[string]any)
		name   string
		// onInput says which side the row changes, and the test asserts that the change took effect on that side
		onInput bool
	}{
		{
			name:    "top level key of the input",
			onInput: true,
			change:  func(in, _ map[string]any) { in[planAttr] = mutated },
		},
		{name: "top level key of the output", change: func(_, out map[string]any) { out[planAttr] = mutated }},
		{
			name: "nested map of the input", onInput: true,
			change: func(in, _ map[string]any) {
				nested, _ := in[orgKey].(map[string]any)
				nested["name"] = mutated
			},
		},
		{
			name: "nested map of the output",
			change: func(_, out map[string]any) {
				nested, _ := out[orgKey].(map[string]any)
				nested["name"] = mutated
			},
		},
		{
			name: "nested slice of the input", onInput: true,
			change: func(in, _ map[string]any) {
				tags, _ := in[tagsKey].([]any)
				tags[0] = mutated
			},
		},
		{
			name: "nested slice of the output",
			change: func(_, out map[string]any) {
				tags, _ := out[tagsKey].([]any)
				tags[0] = mutated
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			want := map[string]any{
				planAttr: proPlan,
				orgKey:   map[string]any{nameKey: acmeOrg},
				tagsKey:  []any{"a", "b"},
			}
			input := map[string]any{
				planAttr: proPlan,
				orgKey:   map[string]any{nameKey: acmeOrg},
				tagsKey:  []any{"a", "b"},
			}

			eval := flagruntime.NewEvaluator(newService(t), &flagruntime.EvaluatorConfig{
				EvaluationContextEnrichment: input,
				FlagSetEnrichment:           map[string]map[string]any{namedSet: input},
				Environment:                 prodEnv,
			})

			output := eval.EvaluationContextEnrichment(nil)
			named := eval.EvaluationContextEnrichment(&flagruntime.FlagSet{Name: namedSet})

			tt.change(input, output)

			// the change took effect on the side the row names (the row is not vacuous), and reached nothing else
			if tt.onInput {
				assert.NotEqual(t, want, input, "the input was changed")
			} else {
				assert.NotEqual(t, want, output, "the output was changed")
			}

			assert.Equal(t, want, eval.EvaluationContextEnrichment(nil))
			assert.Equal(t, want, eval.EvaluationContextEnrichment(&flagruntime.FlagSet{Name: namedSet}))
			assert.Equal(t, want, named, "the copy that was handed out before the change")
		})
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

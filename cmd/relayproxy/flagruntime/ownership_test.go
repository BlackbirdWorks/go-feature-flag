package flagruntime_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
)

// ownedContext is a context that matches the rules of the shapes.
func ownedContext() ffcontext.EvaluationContext {
	return ffcontext.NewEvaluationContextBuilder("user-2").
		AddCustom("beta", true).AddCustom(planAttr, proPlan).AddCustom("country", "FR").Build()
}

// The typed object and array calls hand out a copy that the caller owns: changing it (deeply) never changes the
// flag, whatever the path of the evaluation (plain flag, scheduled flag), and the table is the one it was.
func TestOwnership_TypedJSONResultsAreCopies(t *testing.T) {
	t.Parallel()

	set := shapesSet(t)
	eval := flagruntime.NewEvaluator(newService(t), nil)
	ctx := ownedContext()
	before, err := set.Change()
	require.NoError(t, err)

	tests := []struct {
		call   func() any
		mutate func(any)
		name   string
	}{
		{
			name: "object",
			call: func() any {
				res, _ := eval.JSONVariationDetails(set, objectFlag, ctx, nil)

				return res.Value
			},
			mutate: func(val any) {
				obj, _ := val.(map[string]any)
				obj["name"] = mutated
				nested, _ := obj["nested"].(map[string]any)
				nested["deep"] = []any{mutated}
			},
		},
		{
			name: "array",
			call: func() any {
				res, _ := eval.JSONArrayVariationDetails(set, "array-flag", ctx, nil)

				return res.Value
			},
			mutate: func(val any) {
				arr, _ := val.([]any)
				arr[0] = mutated

				if inner, ok := arr[len(arr)-1].([]any); ok {
					inner[0] = mutated
				}
			},
		},
		{
			name: "scheduled object (the engine copies it already)",
			call: func() any {
				res, _ := eval.JSONVariationDetails(set, "scheduled-object-flag", ctx, nil)

				return res.Value
			},
			mutate: func(val any) {
				obj, _ := val.(map[string]any)
				obj["k"] = mutated
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			first := mustJSON(t, tt.call())

			val := tt.call()
			tt.mutate(val)

			assert.NotEqual(t, first, mustJSON(t, val), "the mutation did change the copy")
			assert.Equal(t, first, mustJSON(t, tt.call()), "the next evaluation is the first one")
		})
	}

	after, err := set.Change()
	require.NoError(t, err)
	assert.Equal(t, before, after, "the flags did not change")
	assert.JSONEq(t, mustJSON(t, shapesSet(t).Configuration(nil)), mustJSON(t, set.Configuration(nil)))
}

// The metadata map of a result is new for every evaluation (the nested containers are shared, the map is not), so a
// caller may add keys to it, and ResponseMetadata does so without touching the flag: the next evaluation, the
// configuration and the hashes are unchanged.
func TestOwnership_TopLevelMetadataIsPerEvaluation(t *testing.T) {
	t.Parallel()

	set := shapesSet(t)
	eval := flagruntime.NewEvaluator(newService(t), nil)
	ctx := ownedContext()

	flags := []string{boolRule, "bool-flat-meta", "bool-disabled", objectFlag}

	for _, flag := range flags {
		t.Run(flag, func(t *testing.T) {
			t.Parallel()

			before, err := set.Change()
			require.NoError(t, err)

			first, _ := eval.RawVariation(set, flag, ctx, nil)
			second, _ := eval.RawVariation(set, flag, ctx, nil)

			require.NotNil(t, first.Metadata)

			first.Metadata["added"] = true
			first.Cacheable = true

			withKeys := eval.ResponseMetadata(&first)
			withKeys["more"] = 1

			assert.NotContains(t, second.Metadata, "added", "the second evaluation has its own map")
			assert.NotContains(t, second.Metadata, flagruntime.LegacyMetadataCacheable)

			third, _ := eval.RawVariation(set, flag, ctx, nil)
			assert.NotContains(t, third.Metadata, "added")
			assert.NotContains(t, third.Metadata, "more")
			assert.NotContains(t, third.Metadata, flagruntime.MetadataCacheable)

			after, err := set.Change()
			require.NoError(t, err)
			assert.Equal(t, before, after, "the flag did not change")

			flag, ok := set.Flag(flag)
			require.True(t, ok)
			assert.NotContains(t, flag.GetMetadata(), "added")
		})
	}
}

// What is shared is shared: the value of a plain evaluation is the value stored in the flag, so a write into it is
// seen by the next evaluation. This is the documented read-only contract (it is why a handler must never write into
// a result), pinned here so that nobody makes the evaluator copy by accident and pay for it on every request. The
// flag set of this test is its own, nothing else reads it.
func TestOwnership_SharedPartsAreTheFlags(t *testing.T) {
	t.Parallel()

	set := shapesSet(t)
	eval := flagruntime.NewEvaluator(newService(t), nil)
	ctx := ownedContext()

	res, err := eval.RawVariation(set, objectFlag, ctx, nil)
	require.NoError(t, err)

	shared, _ := res.Value.(map[string]any)
	require.NotNil(t, shared)

	shared["marker"] = true

	again, _ := eval.RawVariation(set, objectFlag, ctx, nil)
	seen, _ := again.Value.(map[string]any)

	assert.Equal(t, true, seen["marker"], "the value is the flag's own")

	// the helper for a caller that must change a value takes a copy
	own, _ := flagdef.CloneValue(again.Value).(map[string]any)
	own["private"] = true

	last, _ := eval.RawVariation(set, objectFlag, ctx, nil)
	lastValue, _ := last.Value.(map[string]any)

	assert.NotContains(t, lastValue, "private")
}

// The enrichment the evaluations merge is the evaluator's own copy: a caller that changes the map it passed in after
// NewEvaluator does not change the answers.
func TestOwnership_EnrichmentInputChangeDoesNotReachEvaluations(t *testing.T) {
	t.Parallel()

	data := []byte("pro-only:\n  variations:\n    on: true\n    off: false\n  targeting:\n" +
		"    - query: org.name eq \"acme\"\n      variation: on\n  defaultRule:\n    variation: off\n")
	set := runtimeOf(t, data, "yaml")
	input := map[string]any{orgKey: map[string]any{nameKey: acmeOrg}}
	eval := flagruntime.NewEvaluator(newService(t), &flagruntime.EvaluatorConfig{EvaluationContextEnrichment: input})

	before, err := eval.RawVariation(set, "pro-only", ffcontext.NewEvaluationContext("u"), nil)
	require.NoError(t, err)

	nested, _ := input[orgKey].(map[string]any)
	nested["name"] = "other"

	after, err := eval.RawVariation(set, "pro-only", ffcontext.NewEvaluationContext("u"), nil)
	require.NoError(t, err)

	assert.Equal(t, true, before.Value)
	assert.Equal(t, before.Value, after.Value)
}

// The value of a tracked event is not the value the caller gets from a typed call: the exporter keeps the event, so a
// caller that edits its result must not change what is recorded. The cases are the flag itself, a scheduled flag (the
// engine's own copy) and the default of the caller (a flag that is not there is tracked, as in version 1).
func TestOwnership_TypedResultIsNotTheValueOfTheEvent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		call   func(eval *flagruntime.Evaluator, set *flagruntime.FlagSet) map[string]any
		name   string
		want   map[string]any
		change string
	}{
		{
			name: "flag value",
			call: func(eval *flagruntime.Evaluator, set *flagruntime.FlagSet) map[string]any {
				res, _ := eval.JSONVariationDetails(set, objectFlag, ownedContext(), nil)

				return res.Value
			},
			want:   map[string]any{"name": "beta", "nested": map[string]any{"deep": []any{}}},
			change: "name",
		},
		{
			name: "scheduled flag value",
			call: func(eval *flagruntime.Evaluator, set *flagruntime.FlagSet) map[string]any {
				res, _ := eval.JSONVariationDetails(set, "scheduled-tracked-object-flag", ownedContext(), nil)

				return res.Value
			},
			want:   map[string]any{"k": []any{float64(3)}},
			change: "k",
		},
		{
			name: "default value of the caller",
			call: func(eval *flagruntime.Evaluator, set *flagruntime.FlagSet) map[string]any {
				res, _ := eval.JSONVariationDetails(set, missingKey, ownedContext(), map[string]any{"d": 1})

				return res.Value
			},
			want:   map[string]any{"d": 1},
			change: "d",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var events []*exporter.FeatureEvent

			eval := flagruntime.NewEvaluator(newService(t), &flagruntime.EvaluatorConfig{
				TrackEvent: func(event *exporter.FeatureEvent) { events = append(events, event) },
			})

			// the flag of this case must be tracked: objectFlag is, so is the scheduled one, a missing flag is
			got := tt.call(eval, shapesSet(t))
			require.Len(t, events, 1)

			got[tt.change] = mutated

			assert.Equal(t, tt.want, events[0].Value, "what the exporter keeps")
		})
	}
}

// A nil evaluation context is the empty context for a tracked evaluation too: the event is built, it describes an
// empty targeting key, and nothing panics.
func TestEvaluator_TrackedEvaluationWithANilContext(t *testing.T) {
	t.Parallel()

	var events []*exporter.FeatureEvent

	eval := flagruntime.NewEvaluator(newService(t), &flagruntime.EvaluatorConfig{
		TrackEvent: func(event *exporter.FeatureEvent) { events = append(events, event) },
	})

	res, err := eval.RawVariation(shapesSet(t), trackedFlag, nil, nil)
	require.NoError(t, err)
	assert.True(t, res.TrackEvents)

	require.Len(t, events, 1)
	assert.Equal(t, trackedFlag, events[0].Key)
	assert.Empty(t, events[0].UserKey)
	assert.Equal(t, "user", events[0].ContextKind)
}

package flagruntime_test

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
	"github.com/blackbirdworks/semaphore/modules/core/model"
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

	// every row says what it is about: the kind of the value and whether the flag has scheduled steps, asserted
	tests := []struct {
		call          func() any
		mutate        func(any)
		name          string
		flag          string
		wantValueKind string
		wantScheduled bool
	}{
		{
			name: kindObject, flag: objectFlag, wantValueKind: kindMap,
			call: func() any {
				res, _ := eval.JSONVariationDetails(set, objectFlag, ctx, nil)

				return res.Value
			},
			mutate: func(val any) {
				obj, _ := val.(map[string]any)
				obj[nameKey] = mutated
				nested, _ := obj["nested"].(map[string]any)
				nested["deep"] = []any{mutated}
			},
		},
		{
			name: kindArray, flag: arrayFlag, wantValueKind: kindList,
			call: func() any {
				res, _ := eval.JSONArrayVariationDetails(set, arrayFlag, ctx, nil)

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
			name: "scheduled object (the engine copies it already)", flag: scheduledObject,
			wantValueKind: kindMap, wantScheduled: true,
			call: func() any {
				res, _ := eval.JSONVariationDetails(set, scheduledObject, ctx, nil)

				return res.Value
			},
			mutate: func(val any) {
				obj, _ := val.(map[string]any)
				obj["k"] = mutated
			},
		},
	}

	// the comparison of the table runs when every row, which runs in parallel, is done: a cleanup of the parent
	// runs after its subtests, and the check cannot be made before their mutations
	t.Cleanup(func() {
		after, changeErr := set.Change()
		require.NoError(t, changeErr)
		assert.Equal(t, before, after, "the flags did not change")
		assert.JSONEq(t, mustJSON(t, shapesSet(t).Configuration(nil)), mustJSON(t, set.Configuration(nil)))
	})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			flag, found := set.Flag(tt.flag)
			require.True(t, found)
			assert.Equal(t, tt.wantScheduled, flag.Scheduled != nil, "the path of the flag")

			first := mustJSON(t, tt.call())

			val := tt.call()
			assert.Equal(t, tt.wantValueKind, valueKind(val), "the kind of the value")

			tt.mutate(val)

			assert.NotEqual(t, first, mustJSON(t, val), "the mutation did change the copy")
			assert.Equal(t, first, mustJSON(t, tt.call()), "the next evaluation is the first one")
		})
	}
}

// The metadata map of a result is new for every evaluation (the nested containers are shared, the map is not), so a
// caller may add keys to it, and ResponseMetadata does so without touching the flag: the next evaluation, the
// configuration and the hashes are unchanged.
func TestOwnership_TopLevelMetadataIsPerEvaluation(t *testing.T) {
	t.Parallel()

	set := shapesSet(t)
	eval := flagruntime.NewEvaluator(newService(t), nil)
	ctx := ownedContext()

	// the reason of every flag is asserted: the disabled flag takes the disabled path, the others a targeting rule
	flags := []struct {
		name       string
		wantReason string
		// wantNested says that the metadata holds an object or an array (shared with the flag), asserted
		wantNested bool
	}{
		{name: boolRule, wantReason: flagdef.ReasonTargetingMatch, wantNested: true},
		{name: "bool-flat-meta", wantReason: flagdef.ReasonTargetingMatch, wantNested: false},
		{name: "bool-disabled", wantReason: flagdef.ReasonDisabled, wantNested: false},
		{name: objectFlag, wantReason: flagdef.ReasonTargetingMatch, wantNested: true},
	}

	for _, tt := range flags {
		flag := tt.name

		t.Run(flag, func(t *testing.T) {
			t.Parallel()

			before, err := set.Change()
			require.NoError(t, err)

			first, _ := eval.RawVariation(set, flag, ctx, nil)
			second, _ := eval.RawVariation(set, flag, ctx, nil)

			require.NotNil(t, first.Metadata)
			assert.Equal(t, tt.wantReason, first.Reason, "the path of the evaluation")
			assert.Equal(t, tt.wantNested, hasNestedContainer(first.Metadata), "the shape of the metadata")

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

	// every row names its path: the flag it uses, whether that flag has scheduled steps, and the reason and error
	// code of the result, which are asserted, so a fixture that changes cannot leave the row passing on another path
	tests := []struct {
		call          func(eval *flagruntime.Evaluator, set *flagruntime.FlagSet) model.VariationResult[map[string]any]
		want          map[string]any
		name          string
		flag          string
		change        string
		wantReason    string
		wantErrorCode string
		wantScheduled bool
	}{
		{
			name: "flag value", flag: objectFlag, wantReason: flagdef.ReasonTargetingMatch,
			call: func(eval *flagruntime.Evaluator, set *flagruntime.FlagSet) model.VariationResult[map[string]any] {
				res, _ := eval.JSONVariationDetails(set, objectFlag, ownedContext(), nil)

				return res
			},
			want:   map[string]any{nameKey: "beta", "nested": map[string]any{"deep": []any{}}},
			change: "name",
		},
		{
			name: "scheduled flag value", flag: "scheduled-tracked-object-flag", wantScheduled: true,
			wantReason: flagdef.ReasonStatic,
			call: func(eval *flagruntime.Evaluator, set *flagruntime.FlagSet) model.VariationResult[map[string]any] {
				res, _ := eval.JSONVariationDetails(set, "scheduled-tracked-object-flag", ownedContext(), nil)

				return res
			},
			want:   map[string]any{"k": []any{float64(3)}},
			change: "k",
		},
		{
			name: "default value of the caller", flag: missingKey, wantReason: flagdef.ReasonError,
			wantErrorCode: flagdef.ErrorCodeFlagNotFound,
			call: func(eval *flagruntime.Evaluator, set *flagruntime.FlagSet) model.VariationResult[map[string]any] {
				res, _ := eval.JSONVariationDetails(set, missingKey, ownedContext(), map[string]any{"d": 1})

				return res
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

			set := shapesSet(t)

			if flag, found := set.Flag(tt.flag); found {
				assert.Equal(t, tt.wantScheduled, flag.Scheduled != nil, "the flag has scheduled steps")
			} else {
				assert.Equal(t, missingKey, tt.flag, "only the missing flag is not in the table")
			}

			res := tt.call(eval, set)

			require.Len(t, events, 1)

			assert.Equal(t, tt.wantReason, res.Reason)
			assert.Equal(t, tt.wantErrorCode, res.ErrorCode)

			res.Value[tt.change] = mutated

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

// The rule for the event, the same for every entry point: whenever the event would hold a value that the caller owns
// (its default, returned as given, because the flag is not there, is disabled, fails, or holds another type) the event
// gets a snapshot, and neither side can change the other; the JSON of the result is the same with and without the hook.
// The rows are the entry points (the raw call and the two typed calls with a container) by the reasons for a default.
func TestOwnership_TrackedDefaultIsSnapshottedForEveryEntryPoint(t *testing.T) {
	t.Parallel()

	// every row names the cause that its fixture must produce: it is asserted on the result of a plain evaluation, so a
	// fixture that goes missing or changes cannot leave the row passing for another reason
	reasons := []struct {
		name          string
		flag          string
		wantReason    string
		wantErrorCode string
	}{
		{
			name:          "flag not there",
			flag:          missingKey,
			wantReason:    flagdef.ReasonError,
			wantErrorCode: flagdef.ErrorCodeFlagNotFound,
		},
		{name: "disabled flag", flag: "bool-disabled", wantReason: flagdef.ReasonDisabled},
		{
			name: "error of the evaluation", flag: "bucketing-flag", wantReason: flagdef.ReasonError,
			wantErrorCode: flagdef.ErrorCodeTargetingKeyMissing,
		},
		{
			name: "value of another type", flag: boolStatic, wantReason: flagdef.ReasonError,
			wantErrorCode: flagdef.ErrorCodeTypeMismatch,
		},
	}

	entries := []struct {
		call     func(eval *flagruntime.Evaluator, set *flagruntime.FlagSet, flag string, given any) any
		cause    func(eval *flagruntime.Evaluator, set *flagruntime.FlagSet, flag string) (reason, code string)
		newGiven func() any
		change   func(val any)
		name     string
		wantJSON string
		// wantNilCount is the number of nil containers that the default of the row holds, asserted on the fixture
		wantNilCount int
		// noMismatch: the raw call returns whatever type the flag has, so it has no value of another type
		noMismatch bool
	}{
		{
			name: "RawVariation",
			cause: func(eval *flagruntime.Evaluator, set *flagruntime.FlagSet, flag string) (string, string) {
				res, _ := eval.RawVariation(set, flag, ownedContext(), nil)

				return res.Reason, res.ErrorCode
			},
			call: func(eval *flagruntime.Evaluator, set *flagruntime.FlagSet, flag string, given any) any {
				res, _ := eval.RawVariation(set, flag, ownedContext(), given)

				return res.Value
			},
			newGiven: func() any {
				return map[string]any{"items": []any(nil), "obj": map[string]any(nil), "none": nil, listKey: []any{1}}
			},
			change: func(val any) {
				obj, _ := val.(map[string]any)
				obj[listKey] = mutated
			},
			wantJSON:     `{"items":null,"list":[1],"none":null,"obj":null}`,
			wantNilCount: 2,
			noMismatch:   true,
		},
		{
			name: "JSONVariationDetails",
			cause: func(eval *flagruntime.Evaluator, set *flagruntime.FlagSet, flag string) (string, string) {
				res, _ := eval.JSONVariationDetails(set, flag, ownedContext(), nil)

				return res.Reason, res.ErrorCode
			},
			call: func(eval *flagruntime.Evaluator, set *flagruntime.FlagSet, flag string, given any) any {
				def, _ := given.(map[string]any)
				res, _ := eval.JSONVariationDetails(set, flag, ownedContext(), def)

				return res.Value
			},
			newGiven: func() any {
				return map[string]any{"items": []any(nil), "obj": map[string]any(nil), "none": nil, listKey: []any{1}}
			},
			change: func(val any) {
				obj, _ := val.(map[string]any)
				obj[listKey] = mutated
			},
			wantJSON:     `{"items":null,"list":[1],"none":null,"obj":null}`,
			wantNilCount: 2,
		},
		{
			name: "JSONArrayVariationDetails",
			cause: func(eval *flagruntime.Evaluator, set *flagruntime.FlagSet, flag string) (string, string) {
				res, _ := eval.JSONArrayVariationDetails(set, flag, ownedContext(), nil)

				return res.Reason, res.ErrorCode
			},
			call: func(eval *flagruntime.Evaluator, set *flagruntime.FlagSet, flag string, given any) any {
				def, _ := given.([]any)
				res, _ := eval.JSONArrayVariationDetails(set, flag, ownedContext(), def)

				return res.Value
			},
			newGiven: func() any { return []any{[]any(nil), map[string]any(nil), nil, []any{1}} },
			change: func(val any) {
				arr, _ := val.([]any)
				arr[3] = mutated
			},
			wantJSON:     `[null,null,null,[1]]`,
			wantNilCount: 2,
		},
	}

	for _, entry := range entries {
		for _, reason := range reasons {
			if entry.noMismatch && reason.flag == boolStatic {
				continue
			}

			t.Run(entry.name+"/"+reason.name, func(t *testing.T) {
				t.Parallel()

				set := shapesSet(t)

				// without the hook: the result is the default, as given
				plain := flagruntime.NewEvaluator(newService(t), nil)

				gotReason, gotCode := entry.cause(plain, set, reason.flag)
				assert.Equal(t, reason.wantReason, gotReason, "the cause of the default")
				assert.Equal(t, reason.wantErrorCode, gotCode, "the error code of the default")

				untracked := entry.call(plain, set, reason.flag, entry.newGiven())
				assert.JSONEq(t, entry.wantJSON, mustJSON(t, untracked))

				var events []*exporter.FeatureEvent

				eval := flagruntime.NewEvaluator(newService(t), &flagruntime.EvaluatorConfig{
					TrackEvent: func(event *exporter.FeatureEvent) { events = append(events, event) },
				})

				given := entry.newGiven()
				assert.Equal(t, entry.wantNilCount, countContainers(given, true), "the nil containers of the default")

				result := entry.call(eval, set, reason.flag, given)

				require.Len(t, events, 1, "the evaluation is tracked")
				assert.JSONEq(t, entry.wantJSON, mustJSON(t, result), "the result encodes as without the hook")
				assert.JSONEq(t, entry.wantJSON, mustJSON(t, events[0].Value), "the event keeps nil containers nil")
				assert.Equal(
					t,
					reflect.ValueOf(given).Pointer(),
					reflect.ValueOf(result).Pointer(),
					"the default as given",
				)
				assert.NotEqual(t, reflect.ValueOf(given).Pointer(), reflect.ValueOf(events[0].Value).Pointer())

				// the caller changes what it got back: the event does not move
				entry.change(result)
				assert.JSONEq(t, entry.wantJSON, mustJSON(t, events[0].Value), "the event after the caller's change")

				// the exporter changes the event: the default of a caller does not move
				other := entry.newGiven()
				_ = entry.call(eval, set, reason.flag, other)

				require.Len(t, events, 2)
				entry.change(events[1].Value)
				assert.JSONEq(t, entry.wantJSON, mustJSON(t, other), "the default after the exporter's change")
			})
		}
	}
}

// The snapshot keeps the dynamic type and the nil-ness of every container, at the top and at every depth: a nil
// map[string]any, map[any]any or []any stays one (an exporter that reads the value, like the one that writes it to a
// parquet file, sees what it sees for the value of the caller), a container that is not nil is a distinct copy, and a
// value that is not a variation container passes through by reference.
func TestOwnership_SnapshotKeepsTypesAndNils(t *testing.T) {
	t.Parallel()

	byRef := []string{"kept"}

	// every row says what its name claims, and the test asserts it on the fixture and on the event: the kind of the value
	// at the top, how many nil containers it holds at any depth, and how many containers are not variation containers
	tests := []struct {
		given           func() any
		name            string
		wantKind        string
		wantNilCount    int
		wantPassThrough int
		wantNil         bool
	}{
		{
			name: "nil map at the top", given: func() any { return map[string]any(nil) }, wantNil: true,
			wantKind: kindMap, wantNilCount: 1,
		},
		{
			name: "nil any-map at the top", given: func() any { return map[any]any(nil) }, wantNil: true,
			wantKind: kindMap, wantNilCount: 1,
		},
		{
			name: "nil slice at the top", given: func() any { return []any(nil) }, wantNil: true,
			wantKind: kindList, wantNilCount: 1,
		},
		{name: "empty map at the top", given: func() any { return map[string]any{} }, wantKind: kindMap},
		{name: "empty slice at the top", given: func() any { return []any{} }, wantKind: kindList},
		{
			name: "nils at every depth", wantKind: kindMap, wantNilCount: 5,
			given: func() any {
				return map[string]any{
					"a": []any{
						map[string]any(nil),
						[]any(nil),
						map[any]any(nil),
						[]any{map[string]any{"b": []any(nil)}},
					},
					"c": map[any]any{"d": map[string]any(nil)},
				}
			},
		},
		{
			name: "types that are not variation containers pass through", wantKind: kindMap, wantPassThrough: 2,
			given: func() any {
				return map[string]any{"s": byRef, "m": map[string]string{"x": "y"}, "n": 1.5, "t": "text"}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var events []*exporter.FeatureEvent

			eval := flagruntime.NewEvaluator(newService(t), &flagruntime.EvaluatorConfig{
				TrackEvent: func(event *exporter.FeatureEvent) { events = append(events, event) },
			})

			given := tt.given()
			res, _ := eval.RawVariation(shapesSet(t), missingKey, ownedContext(), given)
			require.Len(t, events, 1)

			event := events[0]
			original := exporter.NewFeatureEvent(
				ownedContext(), missingKey, given, res.VariationType, res.Failed, res.Version, "SERVER", nil)

			// the fixture and the event are what the row says
			assert.Equal(t, tt.wantKind, valueKind(given), "the kind of the value at the top")
			assert.Equal(
				t,
				tt.wantNil,
				reflect.ValueOf(given).IsNil(),
				"the top container of the fixture is nil or not",
			)
			assert.Equal(t, tt.wantNilCount, countContainers(given, true), "the nil containers of the fixture")
			assert.Equal(t, tt.wantNilCount, countContainers(event.Value, true), "the nil containers of the event")
			assert.Equal(
				t,
				tt.wantPassThrough,
				countOtherContainers(given),
				"the containers that are not variation ones",
			)

			assertSameShape(t, given, event.Value, "event value")

			if tt.wantNil {
				assert.True(t, reflect.ValueOf(event.Value).IsNil(), "a nil container stays nil")
			}

			// what an exporter that writes parquet files gets from the event is what it gets from the caller's value
			wantParquet, wantErr := original.ConvertValueForParquet()
			gotParquet, gotErr := event.ConvertValueForParquet()

			assert.Equal(t, wantParquet, gotParquet)
			assert.Equal(t, wantErr, gotErr)
		})
	}
}

// assertSameShape compares two values by type and nil-ness, recursively, and checks that containers that are not nil
// are different objects (a copy), except the values that are not variation containers, which are the same.
func assertSameShape(t *testing.T, want, got any, path string) {
	t.Helper()

	require.Equal(t, reflect.TypeOf(want), reflect.TypeOf(got), path)

	wantVal, gotVal := reflect.ValueOf(want), reflect.ValueOf(got)

	switch wantVal.Kind() { //nolint:exhaustive // only the containers are walked
	case reflect.Map, reflect.Slice:
		require.Equal(t, wantVal.IsNil(), gotVal.IsNil(), path+" nil-ness")

		if wantVal.IsNil() {
			return
		}

		require.Equal(t, wantVal.Len(), gotVal.Len(), path+" length")

		switch typed := want.(type) {
		case map[string]any:
			gotMap, _ := got.(map[string]any)

			assert.NotEqual(t, wantVal.Pointer(), gotVal.Pointer(), path+" is a copy")

			for key, elem := range typed {
				assertSameShape(t, elem, gotMap[key], path+"."+key)
			}
		case map[any]any:
			gotMap, _ := got.(map[any]any)

			assert.NotEqual(t, wantVal.Pointer(), gotVal.Pointer(), path+" is a copy")

			for key, elem := range typed {
				assertSameShape(t, elem, gotMap[key], fmt.Sprintf("%s.%v", path, key))
			}
		case []any:
			gotSlice, _ := got.([]any)

			if len(typed) > 0 {
				assert.NotSame(t, &typed[0], &gotSlice[0], path+" is a copy")
			}

			for idx, elem := range typed {
				assertSameShape(t, elem, gotSlice[idx], fmt.Sprintf("%s[%d]", path, idx))
			}
		default:
			// a container of another type is not copied: the same object
			assert.Equal(t, wantVal.Pointer(), gotVal.Pointer(), path+" passes through")
		}
	default:
		assert.Equal(t, want, got, path)
	}
}

// A flag value with nested nulls and empty containers encodes the same through the typed calls (which copy it) and
// through the raw call (which shares it): the copy does not turn a null into a container.
func TestOwnership_NestedNullsInAFlagValueSurviveTheCopy(t *testing.T) {
	t.Parallel()

	set := shapesSet(t)
	eval := flagruntime.NewEvaluator(newService(t), nil)
	want := `{"empty":[],"list":[null,{"y":null}],"x":null}`

	raw, err := eval.RawVariation(set, "object-null-flag", ownedContext(), nil)
	require.NoError(t, err)

	typed, err := eval.JSONVariationDetails(set, "object-null-flag", ownedContext(), nil)
	require.NoError(t, err)

	assert.JSONEq(t, want, mustJSON(t, raw.Value))
	assert.JSONEq(t, want, mustJSON(t, typed.Value))
}

// hasNestedContainer tells whether a metadata map holds an object or an array.
func hasNestedContainer(metadata map[string]any) bool {
	for _, val := range metadata {
		switch val.(type) {
		case map[string]any, map[any]any, []any:
			return true
		}
	}

	return false
}

// countContainers counts the nil containers (nilOnly) or all the containers of a value, at any depth, among the
// variation containers: map[string]any, map[any]any and []any.
func countContainers(val any, nilOnly bool) int {
	count := 0

	visit := func(isNil bool) {
		if !nilOnly || isNil {
			count++
		}
	}

	switch typed := val.(type) {
	case map[string]any:
		visit(typed == nil)

		for _, elem := range typed {
			count += countContainers(elem, nilOnly)
		}
	case map[any]any:
		visit(typed == nil)

		for _, elem := range typed {
			count += countContainers(elem, nilOnly)
		}
	case []any:
		visit(typed == nil)

		for _, elem := range typed {
			count += countContainers(elem, nilOnly)
		}
	}

	return count
}

// countOtherContainers counts the maps and slices inside a value that are not variation containers: they are not
// copied by a snapshot.
func countOtherContainers(val any) int {
	count := 0

	switch typed := val.(type) {
	case map[string]any:
		for _, elem := range typed {
			count += countOtherContainers(elem)
		}
	case map[any]any:
		for _, elem := range typed {
			count += countOtherContainers(elem)
		}
	case []any:
		for _, elem := range typed {
			count += countOtherContainers(elem)
		}
	default:
		switch reflect.ValueOf(val).Kind() { //nolint:exhaustive // only maps and slices count
		case reflect.Map, reflect.Slice:
			count++
		}
	}

	return count
}

// A scheduled flag is evaluated on a copy that the engine memoizes and shares between evaluations. The typed calls
// return a value that the caller may change: changing it, deeply, must not change what the next evaluations read,
// whether the step of the flag is active or still in the future.
func TestOwnership_ScheduledFlagValueIsNotTheMemo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		flag      string
		wantValue string
		wantStep  bool
	}{
		{name: "step active", flag: scheduledObject, wantStep: true, wantValue: `{"k":[3]}`},
		{name: "step in the future", flag: "scheduled-future-object-flag", wantStep: false, wantValue: `{"k":[1,2]}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			set := shapesSet(t)
			eval := flagruntime.NewEvaluator(newService(t), nil)

			flag, found := set.Flag(tt.flag)
			require.True(t, found)
			assert.NotNil(t, flag.Scheduled, "the flag has scheduled steps")

			first, _ := eval.JSONVariationDetails(set, tt.flag, ownedContext(), nil)
			assert.JSONEq(t, tt.wantValue, mustJSON(t, first.Value), "the step state of the row")

			// change the returned value deeply: the top level and the list in it
			first.Value["added"] = mutated

			list, _ := first.Value["k"].([]any)
			list[0] = mutated

			typed, _ := eval.JSONVariationDetails(set, tt.flag, ownedContext(), nil)
			raw, _ := eval.RawVariation(set, tt.flag, ownedContext(), nil)

			assert.JSONEq(t, tt.wantValue, mustJSON(t, typed.Value), "the next typed evaluation")
			assert.JSONEq(t, tt.wantValue, mustJSON(t, raw.Value), "the next raw evaluation")
		})
	}
}

package flagruntime

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
	"github.com/blackbirdworks/semaphore/modules/core/model"
)

const prodName = "prod"

// The frames of the evaluator itself never allocate: the lookups, the decision to copy, the choice of enrichment, the
// event step without a hook and the response metadata of a result that has some. Together with the comparison against
// the engine (TestEvaluator_AllocationsEqualTheEngine, which takes the least cost over many runs because the rule
// engine's [sync.Pool] makes single runs move under the race detector) this says both things: the evaluator adds
// nothing in a run that does not miss the pool, and its own code adds nothing in any run, which no pool touches, so
// an allocation that appears only some of the time in the wrapper would show here.
//
//nolint:paralleltest // AllocsPerRun counts allocations from concurrent tests across the process
func TestEvaluatorWrapperFramesDoNotAllocate(t *testing.T) {
	flagMeta := map[string]any{"k": 1}
	set := &FlagSet{Name: "named", Flags: map[string]*flagdef.InternalFlag{"f": {Metadata: &flagMeta}}}
	none := (*FlagSet)(nil)
	eval := NewEvaluator(nil, &EvaluatorConfig{
		EvaluationContextEnrichment: map[string]any{"a": 1},
		FlagSetEnrichment:           map[string]map[string]any{"named": {"b": 2}},
		Environment:                 prodName,
	})
	plain := NewEvaluator(nil, nil)

	var evalCtx ffcontext.Context = ffcontext.NewEvaluationContext("user")

	tracked := model.VariationResult[any]{TrackEvents: true, Value: "kept"}
	cacheable := model.RawVarResult{Cacheable: true, Metadata: map[string]any{}}
	notCacheable := model.RawVarResult{Metadata: map[string]any{"a": 1}}

	// every row measures a function and keeps what it returned: the result is asserted after the measurement, so the
	// compiler cannot drop the work, and the answer is checked next to the count. The last row is the control: it
	// allocates on purpose, and the harness must see it.
	tests := []struct {
		run        func() any
		want       any
		name       string
		wantAllocs float64
		control    bool
	}{
		{
			name: "FlagSet.Flag", want: true,
			run: func() any {
				_, ok := set.Flag("f")

				return ok
			},
		},
		{
			name: "FlagSet.Flag on a nil flag set", want: false,
			run: func() any {
				_, ok := none.Flag("f")

				return ok
			},
		},
		{
			name: "sharesFlagValue", want: true,
			run: func() any { return sharesFlagValue(set, "f", flagdef.ReasonDefault) },
		},
		{
			name: "sharesFlagValue, default of the caller", want: false,
			run: func() any { return sharesFlagValue(set, "f", flagdef.ReasonError) },
		},
		{
			name: "isDefaultResult", want: true,
			run: func() any { return isDefaultResult(flagdef.ReasonDisabled) },
		},
		{
			name: "enrichmentFor, per flag set", want: map[string]any{"b": 2, "env": prodName},
			run: func() any { return eval.enrichmentFor(set).values },
		},
		{
			name: "enrichmentFor, default", want: map[string]any{"a": 1, "env": prodName},
			run: func() any { return eval.enrichmentFor(none).values },
		},
		{
			name: "notify without a hook", want: "kept",
			run: func() any {
				notify(plain, set, "f", evalCtx, &tracked, true)

				return tracked.Value
			},
		},
		{
			name: "ResponseMetadata, cacheable with a map",
			want: map[string]any{LegacyMetadataCacheable: true, MetadataCacheable: true},
			run:  func() any { return plain.ResponseMetadata(&cacheable) },
		},
		{
			name: "ResponseMetadata, not cacheable", want: map[string]any{"a": 1},
			run: func() any { return plain.ResponseMetadata(&notCacheable) },
		},
		{
			name: "control: a function that allocates", want: "1 2", control: true,
			run: func() any { return fmt.Sprint(1, 2) },
		},
	}

	for _, tt := range tests {
		var sink any

		allocs := testing.AllocsPerRun(200, func() { sink = tt.run() })

		if tt.control {
			assert.Positive(t, allocs, "%s: the harness sees an allocation", tt.name)
		} else {
			assert.InDelta(t, tt.wantAllocs, allocs, 0, tt.name)
		}

		assert.Equal(t, tt.want, sink, tt.name)
	}
}

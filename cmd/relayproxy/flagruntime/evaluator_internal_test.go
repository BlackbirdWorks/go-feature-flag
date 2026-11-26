package flagruntime

import (
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

	tracked := model.VariationResult[any]{TrackEvents: true}
	cacheable := model.RawVarResult{Cacheable: true, Metadata: map[string]any{}}
	notCacheable := model.RawVarResult{Metadata: map[string]any{"a": 1}}

	// what the frames answer: a frame that allocates nothing because it does nothing would pass the counts below
	_, found := set.Flag("f")
	_, foundNil := none.Flag("f")

	assert.True(t, found)
	assert.False(t, foundNil)
	assert.True(t, sharesFlagValue(set, "f", flagdef.ReasonDefault))
	assert.False(t, sharesFlagValue(set, "f", flagdef.ReasonError))
	assert.Equal(t, map[string]any{"b": 2, "env": prodName}, eval.enrichmentFor(set).values)
	assert.Equal(t, map[string]any{"a": 1, "env": prodName}, eval.enrichmentFor(none).values)
	assert.Equal(t, map[string]any{"a": 1}, plain.ResponseMetadata(&notCacheable))
	assert.Equal(t,
		map[string]any{LegacyMetadataCacheable: true, MetadataCacheable: true}, plain.ResponseMetadata(&cacheable))

	tests := []struct {
		run  func()
		name string
	}{
		{name: "FlagSet.Flag", run: func() { _, _ = set.Flag("f") }},
		{name: "FlagSet.Flag on a nil flag set", run: func() { _, _ = none.Flag("f") }},
		{name: "sharesFlagValue", run: func() { _ = sharesFlagValue(set, "f", flagdef.ReasonDefault) }},
		{
			name: "sharesFlagValue, default of the caller",
			run:  func() { _ = sharesFlagValue(set, "f", flagdef.ReasonError) },
		},
		{name: "enrichmentFor, per flag set", run: func() { _ = eval.enrichmentFor(set) }},
		{name: "enrichmentFor, default", run: func() { _ = plain.enrichmentFor(none) }},
		{name: "notify without a hook", run: func() { notify(plain, set, "f", evalCtx, &tracked, true) }},
		{name: "ResponseMetadata, cacheable with a map", run: func() { _ = plain.ResponseMetadata(&cacheable) }},
		{name: "ResponseMetadata, not cacheable", run: func() { _ = plain.ResponseMetadata(&notCacheable) }},
	}

	for _, tt := range tests {
		assert.Zero(t, testing.AllocsPerRun(200, tt.run), tt.name)
	}
}

package flag

import (
	"maps"

	"github.com/thomaspoignant/go-feature-flag/modules/core/ffcontext"
)

// enrichedContext is an evaluation context seen with the evaluation context enrichment merged
// in its custom attributes (the enrichment wins over the attributes with the same name).
// It is a copy-on-enrich view: the context of the caller is never modified, so it can safely
// be shared by concurrent evaluations.
type enrichedContext struct {
	ffcontext.Context
	merged       ffcontext.EvaluationContext
	hasProtected bool
}

func newEnrichedContext(ctx ffcontext.Context, enrichment map[string]any) *enrichedContext {
	merged := ffcontext.NewEvaluationContext(ctx.GetKey())
	custom := merged.GetCustom()
	maps.Copy(custom, ctx.GetCustom())
	maps.Copy(custom, enrichment)
	_, hasProtectedd := enrichment["gofeatureflag"]
	return &enrichedContext{Context: ctx, merged: merged, hasProtected: hasProtectedd}
}

// GetCustom returns the attributes of the context with the enrichment merged in.
func (e *enrichedContext) GetCustom() map[string]any { return e.merged.GetCustom() }

// AddCustomAttribute adds the attribute to the enriched view only.
func (e *enrichedContext) AddCustomAttribute(name string, value any) {
	e.merged.AddCustomAttribute(name, value)
}

// ExtractGOFFProtectedFields reads the protected fields where the enrichment can have set them.
func (e *enrichedContext) ExtractGOFFProtectedFields() ffcontext.GoffContextSpecifics {
	if e.hasProtected {
		return e.merged.ExtractGOFFProtectedFields()
	}
	return e.Context.ExtractGOFFProtectedFields()
}

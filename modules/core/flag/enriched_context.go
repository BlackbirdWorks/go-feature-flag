package flag

import (
	"maps"

	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
)

// enrichedContext is an evaluation context seen with the evaluation context enrichment merged
// in its custom attributes (the enrichment wins over the attributes with the same name).
// It is a copy-on-enrich view: the context of the caller is never modified, so it can safely
// be shared by concurrent evaluations.
type enrichedContext struct {
	ffcontext.Context
	merged       ffcontext.EvaluationContext
	hasProtected bool
	// hasAnonymous is true when the enrichment sets the "anonymous" attribute.
	hasAnonymous bool
}

// EnrichContext returns the context seen with the enrichment merged in its custom attributes
// (see enrichedContext), or the context itself when there is nothing to merge. The context of the
// caller is never modified.
func EnrichContext(ctx ffcontext.Context, enrichment map[string]any) ffcontext.Context {
	if len(enrichment) == 0 {
		return ctx
	}
	return newEnrichedContext(ctx, enrichment)
}

func newEnrichedContext(ctx ffcontext.Context, enrichment map[string]any) *enrichedContext {
	merged := ffcontext.NewEvaluationContext(ctx.GetKey())
	custom := merged.GetCustom()
	maps.Copy(custom, ctx.GetCustom())
	maps.Copy(custom, enrichment)
	hasProtectedd := ffcontext.HasReservedKey(enrichment)
	_, hasAnonymous := enrichment["anonymous"]
	return &enrichedContext{
		Context: ctx, merged: merged, hasProtected: hasProtectedd, hasAnonymous: hasAnonymous,
	}
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

// IsAnonymous is the merged "anonymous" attribute when the enrichment sets it (as it was when the
// enrichment was written in the context of the caller), the status of the context otherwise.
func (e *enrichedContext) IsAnonymous() bool {
	if e.hasAnonymous {
		return e.merged.IsAnonymous()
	}
	return e.Context.IsAnonymous()
}

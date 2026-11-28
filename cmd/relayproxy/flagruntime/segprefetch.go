package flagruntime

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/blackbirdworks/semaphore/cmdhelpers/configfile"
	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/pkgs/evalcore"
)

// lookupKey identifies one membership question.
type lookupKey struct{ segment, id string }

// prefetched is the provider of one evaluation: the memberships that were fetched in one batch before the rules ran.
// A question that was not fetched (an attribute that the batch could not read) goes to the view, as a lookup does.
type prefetched struct {
	view    *SegmentView
	answers map[lookupKey]segment.State
}

// Contains implements [segment.Provider].
func (p *prefetched) Contains(ctx context.Context, lookups []segment.Lookup) error {
	var (
		miss []segment.Lookup
		idx  []int
	)

	for i := range lookups {
		state, ok := p.answers[lookupKey{lookups[i].Segment, lookups[i].ID}]
		if ok {
			lookups[i].State = state

			continue
		}

		miss = append(miss, lookups[i])
		idx = append(idx, i)
	}

	if len(miss) == 0 {
		return nil
	}

	// an error leaves the lookups Unknown, and the evaluation reports STORE_ERROR: it is not returned, for the engine
	// would write a line for every evaluation of an outage (the connection logs it once, the metrics count it)
	_ = p.view.Contains(ctx, miss)

	for n, i := range idx {
		lookups[i].State = miss[n].State
	}

	return nil
}

// batch collects the memberships that the flags of an evaluation need.
type batch struct {
	view    *SegmentView
	attrs   map[string]any
	evalCtx ffcontext.Context
	enrich  map[string]any
	seen    map[lookupKey]struct{}
	lookups []segment.Lookup
}

func (b *batch) add(terms []segment.Term) {
	for i := range terms {
		if !b.view.Serves(terms[i].Name) {
			continue
		}

		if b.attrs == nil {
			// the enrichment of the flag set is merged over the request, as the rules see them
			evalCtx := b.evalCtx
			if len(b.enrich) > 0 {
				evalCtx = flagdef.NewEnrichedContext(evalCtx, b.enrich)
			}

			b.attrs = evalcore.ContextToMap(evalCtx)
		}

		id, ok := attributeID(b.attrs, terms[i].Path)
		if !ok {
			continue
		}

		key := lookupKey{terms[i].Name, id}
		if _, dup := b.seen[key]; dup {
			continue
		}

		if b.seen == nil {
			b.seen = map[lookupKey]struct{}{}
		}

		b.seen[key] = struct{}{}
		b.lookups = append(b.lookups, segment.Lookup{Segment: key.segment, ID: id})
	}
}

// attributeID reads the identifier at the path of the attributes of the context; a typed object on the way is left to
// the lookup of the rule.
func attributeID(attrs map[string]any, path []string) (string, bool) {
	var current any = attrs

	for _, name := range path {
		parent, ok := current.(map[string]any)
		if !ok {
			return "", false
		}

		current = parent[name]
	}

	return flagdef.SegmentID(current)
}

// provider asks the view for everything in one batch, bounded by the resolver, and returns the provider of the
// evaluation. A lookup the batch could not answer is Unknown: the status of the evaluation says STORE_ERROR.
func (b *batch) provider(ctx context.Context) *prefetched {
	if len(b.lookups) == 0 {
		return &prefetched{view: b.view}
	}

	// an error leaves the lookups Unknown, and so STORE_ERROR
	if b.view.traced {
		b.tracedLookups(ctx)
	} else {
		_ = b.view.Contains(ctx, b.lookups)
	}

	answers := make(map[lookupKey]segment.State, len(b.lookups))
	for i := range b.lookups {
		answers[lookupKey{b.lookups[i].Segment, b.lookups[i].ID}] = b.lookups[i].State
	}

	return &prefetched{view: b.view, answers: answers}
}

// prefetchFlag returns the provider of the evaluation of one flag; nil for a flag that uses no served dynamic segment.
func (f *FlagSet) prefetchFlag(
	ctx context.Context,
	evalCtx ffcontext.Context,
	enrich map[string]any,
	key string,
) *prefetched {
	if f == nil || f.Segments == nil || f.Segments.View == nil {
		return nil
	}

	terms, ok := f.dynTerms[key]
	if !ok {
		return nil
	}

	b := batch{view: f.Segments.View, evalCtx: evalCtx, enrich: enrich}
	b.add(terms)

	return b.provider(ctx)
}

// prefetchFlags is prefetchFlag for the flags of an evaluation of many: one batch for all of them. No keys means
// every flag of the set.
func (f *FlagSet) prefetchFlags(
	ctx context.Context,
	evalCtx ffcontext.Context,
	enrich map[string]any,
	keys []string,
) *prefetched {
	if f == nil || f.Segments == nil || f.Segments.View == nil || len(f.dynTerms) == 0 {
		return nil
	}

	b := batch{view: f.Segments.View, evalCtx: evalCtx, enrich: enrich}

	if len(keys) == 0 {
		for _, terms := range f.dynTerms {
			b.add(terms)
		}
	} else {
		for _, key := range keys {
			b.add(f.dynTerms[key])
		}
	}

	return b.provider(ctx)
}

// usesServed reports whether the flag uses a segment that the view serves: only such a flag gets the prefetched
// answers, so that a flag has the same status in a bulk evaluation as in a single one.
func (f *FlagSet) usesServed(key string) bool {
	_, ok := f.dynTerms[key]

	return ok
}

// tracedLookups is the batch of the lookups inside its span, a child of the span of the request that ctx carries.
func (b *batch) tracedLookups(ctx context.Context) {
	ctx, span := otel.GetTracerProvider().Tracer(configfile.OtelTracerName).Start(ctx, spanName)
	defer span.End()

	err := b.view.Contains(ctx, b.lookups)
	describeSpan(span, b.view, b.lookups, err)
}

// spanName is the span around the batch of the memberships of an evaluation.
const spanName = "segmentLookups"

// describeSpan describes the span of a batch. The attributes are the connections (names of the bootstrap file) and
// counts, never a segment, an identifier or the text of an error.
func describeSpan(span trace.Span, view *SegmentView, lookups []segment.Lookup, err error) {
	if !span.IsRecording() {
		return
	}

	unanswered := 0

	for i := range lookups {
		if lookups[i].State == segment.Unknown {
			unanswered++
		}
	}

	result := "answered"

	switch {
	case err != nil:
		result = "failed"

		span.SetStatus(codes.Error, "segment lookups failed")
	case unanswered > 0:
		result = "unanswered"
	}

	span.SetAttributes(
		attribute.StringSlice("semaphore.segment.connections", view.Connections()),
		attribute.Int("semaphore.segment.lookups", len(lookups)),
		attribute.Int("semaphore.segment.unanswered", unanswered),
		attribute.String("semaphore.segment.result", result),
	)
}

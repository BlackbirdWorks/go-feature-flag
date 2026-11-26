package flagdef

import (
	"context"
	"log/slog"
	"strconv"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/rules/parser"
)

const (
	// onErrorMatch is the value of Rule.OnError that makes a rule match when its result is unknown.
	onErrorMatch = "match"
	// onErrorSkip is the value of Rule.OnError that makes a rule not match when its result is unknown (the default).
	onErrorSkip = "skip"
	// statesInline is the number of segment terms of a rule whose states are kept on the stack.
	statesInline = 8
)

// unknownMatches reports whether a rule whose result is unknown matches: that is the policy of the rule when a
// segment lookup could not be answered. A rule that is not valid for it never matches (see validate).
func (r *Rule) unknownMatches() bool {
	return r.OnError != nil && *r.OnError == onErrorMatch
}

// evaluateNikunjySegments is evaluateNikunjy for a rule that has segment() terms.
//
//nolint:nonamedreturns // recover sets the result
func (r *Rule) evaluateNikunjySegments(
	evaluator *parser.Evaluator,
	query string,
	queryCtx *queryContext,
) (success bool) {
	defer func() {
		if rec := recover(); rec != nil {
			queryCtx.svc.logger.ErrorContext(context.Background(), "panic recovered while evaluating query",
				slog.Any("panic", rec),
				slog.String(queryName, query),
			)

			success = false
		}
	}()

	terms := evaluator.SegmentTerms()

	var buf [statesInline]parser.SegmentState

	states := buf[:0]
	if len(terms) > len(buf) {
		states = make([]parser.SegmentState, 0, len(terms))
	}

	states = states[:len(terms)]
	queryCtx.resolveSegments(terms, states)

	match, err := evaluator.ProcessSegments(queryCtx.mapOnce(), states)
	if err != nil {
		queryCtx.svc.logger.Error("error while evaluating the nikunjy query",
			slog.String(queryName, query), slog.Any(errorLiteral, err))

		return false
	}

	return r.matches(match)
}

// matches is whether a rule matches given its three-valued result.
func (r *Rule) matches(match parser.Match) bool {
	return match == parser.MatchYes || match == parser.MatchUnknown && r.unknownMatches()
}

// evaluateSegments is evaluateRule for a query that may have segment() terms. A query that does not is evaluated
// by evaluateRule.
func (r *Rule) evaluateSegments(compiled *compiledQuery, queryCtx *queryContext) bool {
	if compiled.format == JSONLogicQueryFormat {
		if compiled.jlSeg == nil {
			return evaluateRule(compiled, queryCtx)
		}

		return r.evaluateJSONLogicSegments(compiled, queryCtx)
	}

	evaluator, err := queryCtx.svc.nikunjy.get(compiled.trimmed)
	if err != nil || !evaluator.HasSegments() {
		return evaluateRule(compiled, queryCtx)
	}

	return r.evaluateNikunjySegments(evaluator, compiled.trimmed, queryCtx)
}

// resolveSegments sets states[i] to the membership for terms[i] of the evaluation context and records the health
// of the lookups in the queryContext.
//
// An attribute that is missing, empty or not a string or an integer is not a member of anything: it is not looked
// up. A segment that the Service does not define matches nobody. A term whose lookup cannot be answered (no
// provider, a failing provider, a cancelled context) is Unknown, then the failure policy of its segment applies.
// The lookups of the rule are sent to the provider in one call, which is the only allocation: the slice that the
// provider fills.
func (q *queryContext) resolveSegments(terms []parser.SegmentTerm, states []parser.SegmentState) {
	items := q.mapOnce()
	status := segment.StatusHealthy

	var (
		lookups []segment.Lookup
		sent    uint32
	)

	for i := range terms {
		states[i] = parser.SegmentUnknown

		id, ok := attributeID(items, terms[i].Path)
		if !ok {
			states[i] = parser.SegmentNotMember

			continue
		}

		if !q.svc.definesSegment(terms[i].Name) {
			states[i] = parser.SegmentNotMember

			continue
		}

		if q.segments == nil {
			status = status.Worse(segment.StatusNotConfigured)

			continue
		}

		if lookups == nil {
			lookups = make([]segment.Lookup, 0, len(terms))
		}

		lookups = append(lookups, segment.Lookup{Segment: terms[i].Name, ID: id, State: segment.Unknown})
		sent |= 1 << i
	}

	if len(lookups) > 0 {
		status = status.Worse(q.lookupSegments(lookups))
		spreadAnswers(states, lookups, sent)
	}

	for i := range terms {
		states[i] = q.svc.failurePolicy(terms[i].Name, states[i])
	}

	q.segmentStatus = q.segmentStatus.Worse(status)
}

// spreadAnswers copies the answers of the lookups to the states of the terms that were sent, bit i of sent being
// the term i. The lookups are in the order of the terms.
func spreadAnswers(states []parser.SegmentState, lookups []segment.Lookup, sent uint32) {
	next := 0

	for i := range states {
		if sent&(1<<i) != 0 {
			states[i] = lookups[next].State
			next++
		}
	}
}

// definesSegment reports whether the Service defines the segment. A Service without definitions defines every
// segment: the provider decides.
func (s *Service) definesSegment(name string) bool {
	if s.segments == nil {
		return true
	}

	_, ok := s.segments.Lookup(name)

	return ok
}

// lookupSegments sends the lookups to the provider. It returns StatusHealthy when every lookup was answered, and
// resets the states of the lookups when the call failed: a failed call has no answer.
func (q *queryContext) lookupSegments(lookups []segment.Lookup) segment.Status {
	ctx := q.requestContext
	if ctx == nil {
		ctx = context.Background()
	}

	err := q.segments.Contains(ctx, lookups)
	if err != nil {
		q.svc.logger.LogAttrs(ctx, slog.LevelWarn, "segment lookup failed", slog.Any(errorLiteral, err))

		for i := range lookups {
			lookups[i].State = segment.Unknown
		}

		return segment.StatusStoreError
	}

	for i := range lookups {
		if lookups[i].State == segment.Unknown {
			return segment.StatusStoreError
		}
	}

	return segment.StatusHealthy
}

// failurePolicy applies the failure policy of the segment name to an unanswered lookup.
func (s *Service) failurePolicy(name string, state parser.SegmentState) parser.SegmentState {
	def, ok := s.segments.Lookup(name)
	if !ok {
		return state
	}

	return def.Resolve(state)
}

// attributeID returns the value of the attribute at path as the identifier to look up. Strings are used as they
// are, integers are formatted; anything else (missing, empty, boolean, float, list, object) is not an identifier.
func attributeID(items map[string]any, path []string) (string, bool) {
	var current any = items

	for _, name := range path {
		m, ok := current.(map[string]any)
		if !ok {
			return "", false
		}

		current = m[name]
	}

	switch val := current.(type) {
	case string:
		return val, val != ""
	case int:
		return strconv.Itoa(val), true
	case int64:
		return strconv.FormatInt(val, 10), true
	}

	return "", false
}

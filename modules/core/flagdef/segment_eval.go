package flagdef

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/bits"
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

// evaluateNikunjySegments is evaluateNikunjy for a rule that has segment() terms. The evaluator recovers its own
// panics and the provider's are recovered by lookupSegments.
func (r *Rule) evaluateNikunjySegments(evaluator *parser.Evaluator, query string, queryCtx *queryContext) bool {
	terms := evaluator.SegmentTerms()

	var buf [statesInline]parser.SegmentState

	states := buf[:0]
	if len(terms) > len(buf) {
		states = make([]parser.SegmentState, 0, len(terms))
	}

	states = states[:len(terms)]

	match, err := queryCtx.resolveSegments(terms, states, evaluator, nil)
	if err != nil {
		queryCtx.svc.logger.LogAttrs(queryCtx.logContext(), slog.LevelError, "error while evaluating the nikunjy query",
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
		// compiled.segments is only set for a JsonLogic query when it was compiled
		return r.evaluateJSONLogicSegments(compiled, queryCtx)
	}

	// compiled.segments is only set for a Rules query that calls segment(): it has terms when it parses
	evaluator, err := queryCtx.svc.nikunjy.get(compiled.trimmed)
	if err != nil {
		return evaluateRule(compiled, queryCtx)
	}

	return r.evaluateNikunjySegments(evaluator, compiled.trimmed, queryCtx)
}

// logContext is the context of the request, or the background context.
func (q *queryContext) logContext() context.Context {
	if q.requestContext == nil {
		return context.Background()
	}

	return q.requestContext
}

// resolveSegments evaluates a rule that has segment terms and returns its three-valued result. states has one entry
// for each term, in the order of terms.
//
// The rule is evaluated as a rule without segment terms is, left to right with the short circuit of "and" and "or",
// until it reaches a segment term. If it does not reach one, the rule is decided: the provider is not called, the
// evaluation reports no segment status, and an error of an operand is the error that the same rule without segment
// terms has. So the order of the operands matters, and a cheap condition first spares the lookup: in
// `plan eq "free" and k in segment("x")` a plan that is not free decides the rule, in
// `k in segment("x") or plan eq "pro"` the segment is reached first.
//
// When a term is reached, every term of the rule is looked up, in one call, which is the only allocation: the slice
// that the provider fills. A term whose attribute is missing, empty or not an identifier is not a member of anything
// and a segment that the Service does not define matches nobody: neither is looked up. A term whose lookup cannot be
// answered (no provider, a failing or panicking provider, an ended context, an invalid state) is unknown, then the
// failure policy of its segment applies. The rule is then evaluated again, whole, left to right, with the answers:
// an unknown term does not stop the evaluation ("unknown or x" and "unknown and x" evaluate x), an operand that is
// reached and fails makes the rule fail (an error is never an unknown result, whatever Rule.OnError says), and an
// operand that the answers make unnecessary is not evaluated.
func (q *queryContext) resolveSegments(
	terms []parser.SegmentTerm,
	states []parser.SegmentState,
	evaluator *parser.Evaluator,
	skeleton *jlNode,
) (parser.Match, error) {
	matched, reached, err := q.firstPass(evaluator, skeleton)
	if err != nil {
		return parser.MatchNo, err
	}

	if !reached {
		return matchOfBool(matched), nil
	}

	var pending uint32

	for i := range terms {
		states[i] = parser.SegmentUnknown

		if _, ok := q.attributeID(terms[i].Path); !ok || !q.svc.definesSegment(terms[i].Name) {
			states[i] = parser.SegmentNotMember

			continue
		}

		pending |= 1 << i
	}

	if pending != 0 {
		q.lookup(terms, states, pending)
	}

	return q.secondPass(evaluator, skeleton, states)
}

func matchOfBool(matched bool) parser.Match {
	if matched {
		return parser.MatchYes
	}

	return parser.MatchNo
}

// firstPass evaluates the rule until the first segment term, see resolveSegments.
func (q *queryContext) firstPass(evaluator *parser.Evaluator, skeleton *jlNode) (bool, bool, error) {
	if evaluator != nil {
		matched, reached, err := evaluator.ProcessUntilSegment(q.mapOnce())
		if err != nil {
			return false, false, fmt.Errorf("evaluating the rule: %w", err)
		}

		return matched, reached, nil
	}

	return skeleton.plain(q)
}

// secondPass evaluates the whole rule with the answers, see resolveSegments.
func (q *queryContext) secondPass(
	evaluator *parser.Evaluator,
	skeleton *jlNode,
	states []parser.SegmentState,
) (parser.Match, error) {
	if evaluator != nil {
		match, err := evaluator.ProcessSegments(q.mapOnce(), states)
		if err != nil {
			return parser.MatchNo, fmt.Errorf("evaluating the rule: %w", err)
		}

		return match, nil
	}

	return skeleton.match(q, states)
}

// lookup answers the terms of the bits of pending: it sets their states and records the health of the lookups.
func (q *queryContext) lookup(terms []parser.SegmentTerm, states []parser.SegmentState, pending uint32) {
	var status segment.Status

	if q.segments == nil {
		status = segment.StatusNotConfigured
	} else {
		lookups := make([]segment.Lookup, 0, bits.OnesCount32(pending))

		for i := range terms {
			if pending&(1<<i) != 0 {
				id, _ := q.attributeID(terms[i].Path)
				lookups = append(lookups, segment.Lookup{Segment: terms[i].Name, ID: id, State: segment.Unknown})
			}
		}

		status = q.lookupSegments(lookups)

		next := 0

		for i := range terms {
			if pending&(1<<i) != 0 {
				states[i] = parser.SegmentState(lookups[next].State)
				next++
			}
		}
	}

	for i := range terms {
		if pending&(1<<i) != 0 && states[i] == parser.SegmentUnknown {
			states[i] = q.svc.failurePolicy(terms[i].Name, states[i])
		}
	}

	q.segmentStatus = q.segmentStatus.Worse(status)
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

// lookupSegments sends the lookups to the provider and returns the health of the call: StatusHealthy when every
// lookup was answered. A call that fails as a whole (an error, a panic, an ended context) has no answer: every
// state is reset to Unknown. A state that is not one of the three constants is Unknown too. The provider is not
// called when the context of the request has ended.
func (q *queryContext) lookupSegments(lookups []segment.Lookup) segment.Status {
	ctx := q.requestContext
	if ctx == nil {
		ctx = context.Background()
	}

	err := ctx.Err()
	if err == nil {
		err = q.callProvider(ctx, lookups)
	}

	if err != nil {
		q.svc.logger.LogAttrs(ctx, slog.LevelWarn, "segment lookup failed", slog.Any(errorLiteral, err))

		for i := range lookups {
			lookups[i].State = segment.Unknown
		}

		return segment.StatusStoreError
	}

	status := segment.StatusHealthy

	for i := range lookups {
		if !lookups[i].State.Valid() {
			lookups[i].State = segment.Unknown
		}

		if lookups[i].State == segment.Unknown {
			status = segment.StatusStoreError
		}
	}

	return status
}

// errProviderPanic is the error of a provider that panicked.
var errProviderPanic = errors.New("the segment provider panicked")

// callProvider calls the provider, a panic being an error. Only the type of the panic value is reported: it can
// hold identifiers.
func (q *queryContext) callProvider(ctx context.Context, lookups []segment.Lookup) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("%w: %T", errProviderPanic, rec)
		}
	}()

	err = q.segments.Contains(ctx, lookups)
	if err != nil {
		return fmt.Errorf("the segment provider failed: %w", err)
	}

	return nil
}

// failurePolicy applies the failure policy of the segment name to an unanswered lookup.
func (s *Service) failurePolicy(name string, state parser.SegmentState) parser.SegmentState {
	def, ok := s.segments.Lookup(name)
	if !ok {
		return state
	}

	return parser.SegmentState(def.Resolve(segment.State(state)))
}

// attributeID returns the value of the attribute at path as the identifier to look up. Strings are used as they
// are. Integers of every kind are formatted in decimal, and so are the floats and the [json.Number] that hold a
// whole number (the number of a JSON document is a float64): "42", never "4.2e+01" or "42.0". A float is an exact
// integer only up to 2^53, a larger one is the identifier of the number that the float holds, which can differ from
// the number that was written. Everything else (missing, empty, boolean, a number with a fraction, NaN, infinity,
// list, object) is not an identifier.
//
// A context of the Go client can hold an object of another type than map[string]any (a map[string]string, a struct
// with JSON tags): the attribute is read from that object by its own value, named as its JSON form names it, see
// fieldAtPath, so nothing else in the context matters and an integer stays exact.
func (q *queryContext) attributeID(path []string) (string, bool) {
	var current any = q.mapOnce()

	for i, name := range path {
		switch parent := current.(type) {
		case map[string]any:
			current = parent[name]
		case nil:
			return "", false
		default:
			value, ok := fieldAtPath(parent, path[i:])
			if !ok {
				return "", false
			}

			return identifier(value)
		}
	}

	return identifier(current)
}

// identifier is the identifier of a scalar value.
func identifier(current any) (string, bool) {
	if str, ok := current.(string); ok {
		return str, str != ""
	}

	if id, ok := integerID(current); ok {
		return id, true
	}

	switch val := current.(type) {
	case float32:
		return wholeFloatID(float64(val))
	case float64:
		return wholeFloatID(val)
	case json.Number:
		return numberID(val)
	}

	return "", false
}

// integerID formats an integer of any kind.
func integerID(value any) (string, bool) {
	switch val := value.(type) {
	case int:
		return strconv.FormatInt(int64(val), 10), true
	case int8:
		return strconv.FormatInt(int64(val), 10), true
	case int16:
		return strconv.FormatInt(int64(val), 10), true
	case int32:
		return strconv.FormatInt(int64(val), 10), true
	case int64:
		return strconv.FormatInt(val, 10), true
	case uint:
		return strconv.FormatUint(uint64(val), 10), true
	case uint8:
		return strconv.FormatUint(uint64(val), 10), true
	case uint16:
		return strconv.FormatUint(uint64(val), 10), true
	case uint32:
		return strconv.FormatUint(uint64(val), 10), true
	case uint64:
		return strconv.FormatUint(val, 10), true
	}

	return "", false
}

// wholeFloatID formats a float that holds a whole number.
func wholeFloatID(val float64) (string, bool) {
	if math.IsNaN(val) || math.IsInf(val, 0) || val != math.Trunc(val) {
		return "", false
	}

	if val == 0 {
		return "0", true // not "-0"
	}

	return strconv.FormatFloat(val, 'f', 0, 64), true
}

// numberID is the identifier of a [json.Number]: an integer as written, a float that holds a whole number as such.
func numberID(num json.Number) (string, bool) {
	integer, err := num.Int64()
	if err == nil {
		return strconv.FormatInt(integer, 10), true
	}

	float, err := num.Float64()
	if err != nil {
		return "", false
	}

	return wholeFloatID(float)
}

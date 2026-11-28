package flagdef

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/big"
	"strconv"
	"strings"

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
// answered (no provider, a failing or panicking provider, an ended context, an invalid state) is unanswered, then the
// failure policy of its segment applies (not a member by default; unknown only for a segment with OnErrorUnknown).
// The rule is then evaluated again, whole, left to right, with the answers: an unknown term does not stop the
// evaluation ("unknown or x" and "unknown and x" evaluate x), an operand that is reached and fails makes the rule fail
// (an error is never an unknown result, whatever Rule.OnError says), and an operand that the answers make
// unnecessary is not evaluated.
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

	var (
		pending uint32
		ids     [parser.MaxSegmentTerms]string // read once for each term of the evaluation
	)

	for i := range terms {
		states[i] = parser.SegmentUnknown

		id, ok := q.attributeID(terms[i].Path)
		if !ok || !q.defines(terms[i].Name) {
			states[i] = parser.SegmentNotMember

			continue
		}

		ids[i] = id
		pending |= 1 << i
	}

	if pending != 0 {
		q.lookup(terms, states, &ids, pending)
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
func (q *queryContext) lookup(
	terms []parser.SegmentTerm,
	states []parser.SegmentState,
	ids *[parser.MaxSegmentTerms]string,
	pending uint32,
) {
	// the static segments are answered from memory: no call, no allocation
	status, asked, pending := q.answerStatic(terms, states, ids, pending)

	switch {
	case asked == 0:
	case q.segments == nil:
		status = status.Worse(segment.StatusNotConfigured)
	default:
		lookups := make([]segment.Lookup, 0, asked)

		for i := range terms {
			if pending&(1<<i) != 0 {
				lookups = append(lookups, segment.Lookup{Segment: terms[i].Name, ID: ids[i], State: segment.Unknown})
			}
		}

		status = status.Worse(q.lookupSegments(lookups))

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
			states[i] = q.failure(terms[i].Name, states[i])
		}
	}

	q.segmentStatus = q.segmentStatus.Worse(status)
}

// answerStatic answers from the catalog the terms of pending that are static or live segments. It returns the health of
// those answers, the number of terms that are left for the provider and the bits of those terms.
func (q *queryContext) answerStatic(
	terms []parser.SegmentTerm,
	states []parser.SegmentState,
	ids *[parser.MaxSegmentTerms]string,
	pending uint32,
) (segment.Status, int, uint32) {
	var (
		status segment.Status
		asked  int
	)

	for i := range terms {
		if pending&(1<<i) == 0 {
			continue
		}

		state, answer, ok := q.catalog.Answer(terms[i].Name, ids[i])
		if !ok {
			asked++

			continue
		}

		if state == segment.Unknown {
			// a live segment that has no answer yet: the failure policy of the segment decides, as for a store error
			states[i] = q.failure(terms[i].Name, parser.SegmentUnknown)
		} else {
			states[i] = parser.SegmentState(state)
		}

		status = status.Worse(answer)
		pending &^= 1 << i
	}

	return status, asked, pending
}

// catalogOf is the catalog of an evaluation: the one of the Context, else the one of the Service.
func catalogOf(svc *Service, flagCtx *Context) *segment.Catalog {
	if flagCtx.SegmentCatalog != nil {
		return flagCtx.SegmentCatalog
	}

	return svc.segments
}

// defines reports whether the catalog defines the segment. Without definitions every segment is defined: the
// provider decides.
func (q *queryContext) defines(name string) bool {
	if q.catalog == nil {
		return true
	}

	return q.catalog.Has(name)
}

// lookupSegments sends the lookups to the provider and returns the health of the call: StatusHealthy when every
// lookup was answered. A provider that returns an error may have answered some lookups first: the answers that are
// Member or NotMember are kept, the others are Unknown, and the health is a store error. A state that is not one of
// the three constants is Unknown. A provider that panics is not trusted: every state is reset to Unknown. The provider
// is not called when the context of the request has ended, unless it is an [segment.EndedContextProvider]; the end of
// the request is not logged as a failure.
func (q *queryContext) lookupSegments(lookups []segment.Lookup) segment.Status {
	ctx := q.requestContext
	if ctx == nil {
		ctx = context.Background()
	}

	var stale bool

	err := ctx.Err()
	ended := err != nil

	if !ended || q.answersWhenEnded() {
		stale, err = q.callProvider(ctx, lookups)
	}

	if err != nil {
		// a request that ended is not a failure of the store: the lookups it did not get stay unanswered, with no line
		// (one for each term of a bulk request would flood the log of a slow store that clients leave)
		if !ended || !errors.Is(err, ctx.Err()) {
			q.svc.logger.LogAttrs(ctx, slog.LevelWarn, "segment lookup failed", slog.Any(errorLiteral, err))
		}

		// whatever the source of the error (the store, or the end of the request during the call), the answers that
		// were set validly are kept; only a provider that panicked is not trusted
		if errors.Is(err, errProviderPanic) {
			for i := range lookups {
				lookups[i].State = segment.Unknown
			}
		}

		sanitize(lookups)

		return segment.StatusStoreError
	}

	status := segment.StatusHealthy
	if stale {
		status = segment.StatusStale
	}

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

// answersWhenEnded reports whether the provider answers from memory when the context of the request has ended.
func (q *queryContext) answersWhenEnded() bool {
	_, ok := q.segments.(segment.EndedContextProvider)

	return ok
}

// sanitize makes every state that is not one of the three constants Unknown.
func sanitize(lookups []segment.Lookup) {
	for i := range lookups {
		if !lookups[i].State.Valid() {
			lookups[i].State = segment.Unknown
		}
	}
}

// errProviderPanic is the error of a provider that panicked.
var errProviderPanic = errors.New("the segment provider panicked")

// callProvider calls the provider, a panic being an error. Only the type of the panic value is reported: it can
// hold identifiers.
func (q *queryContext) callProvider(ctx context.Context, lookups []segment.Lookup) (bool, error) {
	var (
		stale bool
		err   error
	)

	func() {
		defer func() {
			if rec := recover(); rec != nil {
				stale, err = false, fmt.Errorf("%w: %T", errProviderPanic, rec)
			}
		}()

		callErr := q.segments.Contains(ctx, lookups)
		if callErr != nil {
			err = fmt.Errorf("the segment provider failed: %w", callErr)

			return
		}

		// Stale is part of the provider: a panic here is a provider that panicked, and is not trusted
		reporter, ok := q.segments.(segment.StaleReporter)
		stale = ok && reporter.Stale()
	}()

	return stale, err
}

// failure applies the failure policy of the segment name to an unanswered lookup.
func (q *queryContext) failure(name string, state parser.SegmentState) parser.SegmentState {
	return parser.SegmentState(q.catalog.ResolveFor(name, segment.State(state)))
}

// attributeID returns the value of the attribute at path as the identifier to look up. Strings are used as they
// are. Integers of every kind are formatted in decimal, and so are the floats and the [json.Number] that hold a
// whole number (the number of a JSON document is a float64): "42", never "4.2e+01" or "42.0". A float is an exact
// integer only up to 2^53, a larger one is the identifier of the number that the float holds, which can differ from
// the number that was written. Everything else (missing, empty, boolean, a number with a fraction, NaN, infinity,
// list, object) is not an identifier.
//
// A context of the Go client can hold an object of another type than map[string]any (a map[string]string, a struct
// with JSON tags, a pointer): the typed object is converted with encoding/json, so its fields are named, promoted
// and left out exactly as its JSON form does, a number keeps the digits that JSON prints for it (integers of any size
// are exact; a float32 holds what JSON prints for it, 100000008 is 100000010, so use integers, strings or float64
// for identifiers), and a value that cannot be encoded (a cycle, a NaN, a channel, a marshaler that panics) makes the
// attribute unreadable: no identifier, no lookup. Only the typed object that holds the attribute is converted, once
// for the evaluation, nothing else of the context, and nothing is kept after the evaluation.
func (q *queryContext) attributeID(path []string) (string, bool) {
	var current any = q.mapOnce()

	for i, name := range path {
		switch parent := current.(type) {
		case map[string]any:
			current = parent[name]
		case nil:
			return "", false
		default:
			value, ok := q.typedAttribute(path[:i], parent, path[i:])
			if !ok {
				return "", false
			}

			return identifier(value)
		}
	}

	return identifier(current)
}

// typedAttribute returns the value at rest of the JSON form of the typed object found at the path prefix. The object
// is converted once for the evaluation: the result, or the failure, is kept in the queryContext.
func (q *queryContext) typedAttribute(prefix []string, object any, rest []string) (any, bool) {
	key := strings.Join(prefix, "\x00")

	converted, seen := q.typed[key]
	if !seen {
		converted = convertTyped(object)

		if q.typed == nil {
			q.typed = map[string]typedObject{}
		}

		q.typed[key] = converted
	}

	if !converted.ok {
		return nil, false
	}

	return walkDecoded(converted.value, rest)
}

// typedObject is the JSON form of a typed object, decoded, or the fact that it cannot be read.
type typedObject struct {
	value any
	ok    bool
}

// convertTyped converts a typed object to its decoded JSON form, numbers as [json.Number]. A panic of the conversion
// (a nil pointer embedded in the struct whose type has a MarshalJSON method, a marshaler that panics) is an
// unreadable object: the panic value is not looked at, it can hold an identifier.
//
//nolint:nonamedreturns // recover sets the result
func convertTyped(object any) (converted typedObject) {
	defer func() {
		if recover() != nil {
			converted = typedObject{}
		}
	}()

	encoded, err := json.Marshal(object)
	if err != nil {
		return typedObject{}
	}

	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()

	var value any

	err = decoder.Decode(&value)
	if err != nil {
		return typedObject{}
	}

	return typedObject{value: value, ok: true}
}

// walkDecoded returns the value at path of a decoded JSON document.
func walkDecoded(root any, path []string) (any, bool) {
	current := root

	for _, name := range path {
		parent, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}

		current = parent[name]
	}

	return current, true
}

// SegmentID is the identifier that a segment lookup uses for the scalar value of an attribute, and false for a value
// that is not an identifier (a missing, empty or non scalar value). A relay uses it to look the members up before the
// rules are evaluated.
func SegmentID(value any) (string, bool) { return identifier(value) }

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

// maxNumberLen is the longest [json.Number] that is read as an identifier.
const maxNumberLen = 64

// numberID is the identifier of a [json.Number]: the digits of a whole number, exactly, whatever its size and the way
// it is written ("1.0" and "1e3" are "1" and "1000"). A number with a fraction is not an identifier.
func numberID(num json.Number) (string, bool) {
	if len(num) > maxNumberLen || exponentTooLarge(string(num)) {
		return "", false
	}

	exact, ok := new(big.Rat).SetString(string(num))
	if !ok || !exact.IsInt() {
		return "", false
	}

	return exact.Num().String(), true
}

// maxExponent is the largest exponent of a [json.Number] that is read as an identifier: 10^400 is far beyond any id,
// and a larger one would make the conversion expensive.
const maxExponent = 400

func exponentTooLarge(num string) bool {
	_, exponent, found := strings.Cut(strings.ToLower(num), "e")
	if !found {
		return false
	}

	value, err := strconv.Atoi(strings.TrimPrefix(exponent, "+"))

	return err != nil || value > maxExponent || value < -maxExponent
}

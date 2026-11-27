package flagdef

import (
	"encoding/json"
	"strings"

	"github.com/blackbirdworks/semaphore/pkgs/evalcore"
	"github.com/blackbirdworks/semaphore/rules/parser"
)

// compiledQuery is the trimmed form, the format and the decoded JsonLogic rule of a raw query.
type compiledQuery struct {
	jsonLogic any
	// jlSeg is the compiled form of a JsonLogic query that has inSegment operations, nil otherwise.
	jlSeg *jlSegments
	// segErr is why the segment terms of the query are not valid, nil when they are (or there is none).
	segErr  error
	trimmed string
	format  QueryFormat
	// segments is true when the query may have segment terms: evaluating it takes the segment path.
	segments bool
}

// compiledQuery returns the compiled form of raw. It is memoized per raw query by the Service: only
// evaluation stores entries, so the set is bounded by the flag config that the Service evaluates.
func (s *Service) compiledQuery(raw string) *compiledQuery {
	if v, ok := loadPtr[compiledQuery](&s.compiled, raw); ok {
		return v
	}

	compiled := newCompiledQuery(raw)

	actual, _ := s.compiled.LoadOrStore(raw, compiled)

	return ptrOr(actual, compiled)
}

// newCompiledQuery compiles raw without memoizing it: the validation of a flag compiles the queries it checks with
// it, so that a query that is replaced by a reload does not stay in the Service.
func newCompiledQuery(raw string) *compiledQuery {
	trimmed := evalcore.StrTrim(raw)

	compiled := &compiledQuery{trimmed: trimmed, format: queryFormatOf(trimmed)}
	if compiled.format == JSONLogicQueryFormat {
		var rule any
		if json.Unmarshal([]byte(compiled.trimmed), &rule) == nil {
			compiled.jsonLogic = rule
			compiled.jlSeg, compiled.segErr = compileJSONLogicSegments(rule)
			compiled.segments = compiled.jlSeg != nil
		}
	} else {
		compiled.segments = strings.Contains(trimmed, "segment") && parser.HasSegmentCall(trimmed)
	}

	return compiled
}

package flag

import (
	"encoding/json"

	"github.com/blackbirdworks/semaphore/modules/core/utils"
)

// compiledQuery is the trimmed form, the format and the decoded JsonLogic rule of a raw query.
type compiledQuery struct {
	jsonLogic any
	trimmed   string
	format    QueryFormat
}

// compiledQuery returns the compiled form of raw. It is memoized per raw query by the Service: only
// evaluation stores entries, so the set is bounded by the flag config that the Service evaluates.
func (s *Service) compiledQuery(raw string) *compiledQuery {
	if v, ok := s.compiled.Load(raw); ok {
		return v.(*compiledQuery) //nolint:errcheck // only compiledQuery values are stored
	}
	trimmed := utils.StrTrim(raw)
	c := &compiledQuery{trimmed: trimmed, format: queryFormatOf(trimmed)}
	if c.format == JSONLogicQueryFormat {
		var rule any
		if json.Unmarshal([]byte(c.trimmed), &rule) == nil {
			c.jsonLogic = rule
		}
	}
	actual, _ := s.compiled.LoadOrStore(raw, c)

	return actual.(*compiledQuery) //nolint:errcheck // only compiledQuery values are stored
}

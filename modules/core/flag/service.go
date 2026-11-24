package flag

import "sync"

// Service owns the state that evaluating and validating flags needs, so that no package-level
// variable holds it: the parsed nikunjy queries, the memo of the scheduled rollouts, the
// compiled JsonLogic queries and the pool of the query context maps.
//
// A client creates one Service and hands it to everything that validates or evaluates its flags
// (the cache that validates the flags it loads, and the evaluations through Context.Service). Two
// Services never share state, so the clients of a process (e.g. the flag sets of the relay proxy)
// neither contend on each other nor evict each other's entries. A Service is safe for concurrent
// use. Its caches are released when it is garbage collected; Close releases them earlier.
type Service struct {
	nikunjy   *nikunjyEvaluatorCache
	scheduled scheduledMemo
	// compiled memoizes the compiled form of the queries, see compiledQuery.
	compiled sync.Map // map[string]*compiledQuery
	// queryMaps recycles the maps of the queryContexts: a map is only read by the query engines
	// during an evaluation and never retained, so it is cleared and reused by the next evaluation.
	queryMaps sync.Pool
}

// NewService creates an empty Service.
func NewService() *Service {
	return &Service{
		nikunjy: newNikunjyEvaluatorCache(),
		queryMaps: sync.Pool{
			New: func() any { return make(map[string]any, queryMapSize) },
		},
	}
}

// queryMapSize is the initial size of the maps that queryMaps hands out.
const queryMapSize = 16

// Close releases the memoized queries and flags. The Service stays usable: it rebuilds what it
// needs on the next use.
func (s *Service) Close() {
	if s == nil {
		return
	}
	s.nikunjy.reset()
	s.scheduled.entries.Clear()
	s.compiled.Clear()
}

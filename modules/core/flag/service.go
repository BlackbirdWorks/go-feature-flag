package flag

import (
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/blackbirdworks/semaphore/modules/core/clock"
	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/legacy"
)

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
	// queryMaps recycles the maps of the queryContexts: a map is only read by the query engines
	// during an evaluation and never retained, so it is cleared and reused by the next evaluation.
	queryMaps sync.Pool
	nikunjy   *nikunjyEvaluatorCache
	// logger receives the errors of the evaluation (a query that does not parse or panics, ...).
	logger *slog.Logger
	// legacy reports, once, the use of a legacy name that the evaluation reads (the legacy
	// evaluation context attribute).
	legacy    *legacy.Warner
	scheduled scheduledMemo
	// compiled memoizes the compiled form of the queries, see compiledQuery.
	compiled sync.Map // map[string]*compiledQuery
}

// ServiceOption customizes the Service that NewService creates.
type ServiceOption func(*Service)

// WithLogger sets the logger of the Service. Without it the Service does not log.
func WithLogger(logger *slog.Logger) ServiceOption {
	return func(s *Service) {
		if logger != nil {
			s.logger = logger
		}
	}
}

// errNoService is reported by the evaluations that were not given a Service.
var errNoService = errors.New("no flag.Service: pass the Service of the client in flag.Context.Service")

// NewService creates an empty Service.
func NewService(opts ...ServiceOption) *Service {
	svc := &Service{
		nikunjy: newNikunjyEvaluatorCache(),
		queryMaps: sync.Pool{
			New: func() any { return make(map[string]any, queryMapSize) },
		},
		logger: slog.New(slog.DiscardHandler),
	}
	for _, opt := range opts {
		opt(svc)
	}

	svc.legacy = legacy.NewWarner(func(kind, message string) {
		svc.logger.Warn(
			"legacy configuration setting used",
			slog.String("legacyName", kind),
			slog.String("warning", message),
		)
	})

	return svc
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

// evaluationDate is the date of an evaluation: the currentDateTime of the context, or the clock.
// It also reports, once, that the context uses the legacy attribute name.
func (s *Service) evaluationDate(ctx ffcontext.Context) time.Time {
	specifics := ctx.ExtractGOFFProtectedFields()
	if specifics.UsedLegacyKey {
		s.legacy.Warn(legacy.KindEvaluationContext, "the evaluation context attribute \""+
			ffcontext.LegacyReservedKey+"\" is a legacy name, it is still accepted but \""+
			ffcontext.ReservedKey+"\" is preferred")
	}

	if specifics.CurrentDateTime == nil {
		return clock.Now()
	}

	return *specifics.CurrentDateTime
}

package flagdef

import (
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
)

// segmentQueries holds the compiled form of the queries of the validated rules that have segment terms, so that the
// first evaluation after a load compiles nothing. An entry lives as long as a rule list that it was validated with:
// when a reload drops the flags, the lists become unreachable, and a query that no remaining list uses is removed.
// A query that is not here (a scheduled step brings it, or the rule was never validated) is compiled and memoized by
// the Service when it is first evaluated.
type segmentQueries struct {
	refs    map[string]int
	entries sync.Map // map[string]*compiledQuery
	// epoch counts the resets: the cleanup of a list that was retained before a reset must not remove what was
	// retained after it.
	epoch atomic.Uint64
	mu    sync.Mutex
}

// load returns the compiled query of a retained query text.
func (r *segmentQueries) load(raw string) (*compiledQuery, bool) {
	return loadPtr[compiledQuery](&r.entries, raw)
}

// refCount is the number of rule lists that retain a query.
func (r *segmentQueries) refCount(raw string) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.refs[raw]
}

// size is the number of retained queries.
func (r *segmentQueries) size() int {
	size := 0

	r.entries.Range(func(_, _ any) bool {
		size++

		return true
	})

	return size
}

// retain compiles the segment queries of the enabled rules and keeps them until rules is garbage collected.
func (r *segmentQueries) retain(rules *[]Rule) {
	if rules == nil {
		return
	}

	epoch := r.epoch.Load()

	var raws []string

	for i := range *rules {
		rule := &(*rules)[i]
		if rule.IsDisable() || rule.Query == nil || !mayHaveSegments(*rule.Query) {
			continue
		}

		raw := *rule.Query

		compiled := newCompiledQuery(raw)
		if !compiled.segments || compiled.segErr != nil {
			continue
		}

		r.mu.Lock()
		if r.refs == nil {
			r.refs = map[string]int{}
		}

		r.refs[raw]++
		r.mu.Unlock()

		r.entries.LoadOrStore(raw, compiled)

		raws = append(raws, raw)
	}

	if len(raws) > 0 {
		runtime.AddCleanup(rules, func(texts []string) { r.release(epoch, texts) }, raws)
	}
}

// release drops one reference to each query, and the entry of a query that nothing references any more.
func (r *segmentQueries) release(epoch uint64, raws []string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.epoch.Load() != epoch {
		return
	}

	for _, raw := range raws {
		r.refs[raw]--
		if r.refs[raw] <= 0 {
			delete(r.refs, raw)
			r.entries.Delete(raw)
		}
	}
}

// reset drops everything.
func (r *segmentQueries) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.epoch.Add(1)
	clear(r.refs)
	r.entries.Clear()
}

// mayHaveSegments is a cheap test of a query text before it is compiled: it names segment() or inSegment, in the
// text or, for a JsonLogic query that writes a character as a JSON escape (for example "\u0069nSegment"), in the
// decoded document. The compile decides; this only skips the queries that cannot have a segment term.
func mayHaveSegments(raw string) bool {
	return strings.Contains(raw, "segment") || strings.Contains(raw, opInSegment) || strings.Contains(raw, `\u`)
}

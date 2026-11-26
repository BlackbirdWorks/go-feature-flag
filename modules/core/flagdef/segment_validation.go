package flagdef

import (
	"cmp"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/rules/parser"
)

// ruleLabel names the rule index of a rule list in the validation errors: rule "name" (index 2).
func ruleLabel(index int, rule *Rule) string {
	if name := rule.GetName(); name != "" {
		return fmt.Sprintf("targeting rule %q (index %d)", name, index)
	}

	return fmt.Sprintf("targeting rule (index %d)", index)
}

// wrapSegmentError names where the error of a segment term is. The other errors are returned as they are.
func wrapSegmentError(err error, where string) error {
	if errors.Is(err, ErrInvalidSegment) || errors.Is(err, errSegmentInDefaultRule) ||
		errors.Is(err, errInvalidOnError) {
		return fmt.Errorf("%s: %w", where, err)
	}

	return err
}

// validateOnError checks the onError of the rule.
func (r *Rule) validateOnError() error {
	if r.OnError == nil || *r.OnError == onErrorSkip || *r.OnError == onErrorMatch {
		return nil
	}

	return fmt.Errorf("%w, got %q", errInvalidOnError, *r.OnError)
}

// hasSegmentTerm reports whether the query uses a segment term, valid or not. A JsonLogic query uses one when the
// operation inSegment is a key of its document, not when the text appears in a string; a Rules query when it calls
// segment().
func hasSegmentTerm(svc *Service, trimmed string) bool {
	if queryFormatOf(trimmed) == JSONLogicQueryFormat {
		return hasInSegment(svc.compiledQuery(trimmed).jsonLogic)
	}

	return strings.Contains(trimmed, "segment") && parser.HasSegmentCall(trimmed)
}

// queryTerms returns the segment terms of a rule, nil if it has none or if the query is not valid.
func (s *Service) queryTerms(rule *Rule, ev *parser.Evaluator) []parser.SegmentTerm {
	if ev != nil {
		return ev.SegmentTerms()
	}

	if rule.Query == nil {
		return nil
	}

	trimmed := rule.GetTrimmedQuery()

	if queryFormatOf(trimmed) == JSONLogicQueryFormat {
		if compiled := s.compiledQuery(rule.GetQuery()); compiled.jlSeg != nil {
			return compiled.jlSeg.terms
		}

		return nil
	}

	if !strings.Contains(trimmed, "segment") {
		return nil
	}

	evaluator, err := s.nikunjy.get(trimmed)
	if err != nil {
		return nil
	}

	return evaluator.SegmentTerms()
}

// warnUndefinedSegments logs a warning for each segment of the rule that the Service does not define: it matches
// nobody. Nothing is logged when the Service has no segment definitions at all.
func (s *Service) warnUndefinedSegments(rule *Rule, index int, ev *parser.Evaluator) {
	if s.segments == nil {
		return
	}

	for _, term := range s.queryTerms(rule, ev) {
		if _, ok := s.segments.Lookup(term.Name); !ok {
			s.logger.Warn("the rule uses a segment that is not defined: it matches nobody",
				slog.String("rule", ruleLabel(index, rule)), slog.String("segment", term.Name))
		}
	}
}

// SegmentTerms returns the distinct segment terms (segment name and attribute path) that the targeting rules of the
// flag use, including the rules that its scheduled steps add, sorted by segment then attribute. A relay can look
// them up for a request before it evaluates the flag. It parses the queries: call it when a flag is loaded, not for
// each evaluation.
func (f *InternalFlag) SegmentTerms(svc *Service) []segment.Term {
	var terms []segment.Term

	collect := func(rules []Rule) {
		for i := range rules {
			for _, term := range svc.queryTerms(&rules[i], nil) {
				terms = append(terms, segment.Term{Name: term.Name, Path: term.Path})
			}
		}
	}

	collect(f.GetRules())

	if f.Scheduled != nil {
		for i := range *f.Scheduled {
			collect((*f.Scheduled)[i].GetRules())
		}
	}

	compare := func(a, b segment.Term) int {
		return cmp.Or(strings.Compare(a.Name, b.Name), slices.Compare(a.Path, b.Path))
	}

	slices.SortFunc(terms, compare)

	return slices.CompactFunc(terms, func(a, b segment.Term) bool { return compare(a, b) == 0 })
}

// SegmentNames returns the sorted names of the segments that the targeting rules of the flag use, including the
// rules that its scheduled steps add. It parses the queries: call it when a flag is loaded, not for each evaluation.
func (f *InternalFlag) SegmentNames(svc *Service) []string {
	terms := f.SegmentTerms(svc)
	names := make([]string, 0, len(terms))

	for _, term := range terms {
		names = append(names, term.Name)
	}

	return slices.Compact(names)
}

// UsesSegments reports whether a targeting rule of the flag uses a segment.
func (f *InternalFlag) UsesSegments(svc *Service) bool {
	return len(f.SegmentTerms(svc)) > 0
}

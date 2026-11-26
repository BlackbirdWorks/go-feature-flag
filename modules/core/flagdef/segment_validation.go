package flagdef

import (
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

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
	if errors.Is(err, ErrInvalidSegment) || errors.Is(err, errSegmentInDefaultRule) {
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

// hasSegmentTerm reports whether the query uses a segment term, valid or not.
func hasSegmentTerm(svc *Service, trimmed string) bool {
	if queryFormatOf(trimmed) == JSONLogicQueryFormat {
		return strings.Contains(trimmed, opInSegment)
	}

	if !strings.Contains(trimmed, "segment") {
		return false
	}

	ev, err := svc.nikunjy.get(trimmed)
	if err != nil {
		return errors.Is(err, ErrInvalidSegment)
	}

	return ev.HasSegments()
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
		if !strings.Contains(trimmed, opInSegment) {
			return nil
		}

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
func (s *Service) warnUndefinedSegments(rule *Rule, label string, ev *parser.Evaluator) {
	if s.segments == nil {
		return
	}

	for _, term := range s.queryTerms(rule, ev) {
		if _, ok := s.segments.Lookup(term.Name); !ok {
			s.logger.Warn("the rule uses a segment that is not defined: it matches nobody",
				slog.String("rule", label), slog.String("segment", term.Name))
		}
	}
}

// SegmentNames returns the sorted names of the segments that the targeting rules of the flag use, including the
// rules that its scheduled steps add. It parses the queries: call it when a flag is loaded, not for each evaluation.
func (f *InternalFlag) SegmentNames(svc *Service) []string {
	var names []string

	collect := func(rules []Rule) {
		for i := range rules {
			for _, term := range svc.queryTerms(&rules[i], nil) {
				names = append(names, term.Name)
			}
		}
	}

	collect(f.GetRules())

	if f.Scheduled != nil {
		for i := range *f.Scheduled {
			collect((*f.Scheduled)[i].GetRules())
		}
	}

	slices.Sort(names)

	return slices.Compact(names)
}

// UsesSegments reports whether a targeting rule of the flag uses a segment.
func (f *InternalFlag) UsesSegments(svc *Service) bool {
	return len(f.SegmentNames(svc)) > 0
}

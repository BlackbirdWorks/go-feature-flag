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

// jsonWhitespace is the whitespace that JSON allows before a value; the query trim removes spaces only.
const jsonWhitespace = " \t\r\n"

// hasSegmentTerm reports whether the query uses a segment term, valid or not. A JsonLogic query uses one when the
// operation inSegment is a key of its document, not when the text appears in a string; a Rules query when it calls
// segment().
func hasSegmentTerm(trimmed string) bool {
	if queryFormatOf(trimmed) == JSONLogicQueryFormat {
		return hasInSegment(newCompiledQuery(trimmed).jsonLogic)
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
		if compiled := newCompiledQuery(rule.GetQuery()); compiled.jlSeg != nil {
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

// validateScheduledSegments checks the segment terms of the rules that the scheduled steps carry, and their onError:
// a step replaces the query of a rule when it is due, and nothing else validates the result. The steps are partial
// updates, so only what they set is checked, and only the queries that call segment().
func (f *InternalFlag) validateScheduledSegments(svc *Service) error {
	if f.Scheduled == nil {
		return nil
	}

	for i := range *f.Scheduled {
		step := &(*f.Scheduled)[i]
		where := fmt.Sprintf("scheduled step %d", i)

		// the parsed queries of the step live as long as its rules, like those of the rules of the flag
		svc.nikunjy.retain(step.Rules, nil)

		rules := step.GetRules()
		for j := range rules {
			err := rules[j].validateStepSegments(svc, false)
			if err != nil {
				return fmt.Errorf("%s: %w", where, wrapSegmentError(err, ruleLabel(j, &rules[j])))
			}
		}

		if step.DefaultRule != nil {
			err := step.DefaultRule.validateStepSegments(svc, true)
			if err != nil {
				return fmt.Errorf("%s: %w", where, wrapSegmentError(err, "defaultRule"))
			}
		}
	}

	return nil
}

// validateStepSegments checks the onError of a rule of a scheduled step and, when its query calls segment(), the
// query, which stays parsed in the Service as long as the rules of the step.
func (r *Rule) validateStepSegments(svc *Service, isDefault bool) error {
	err := r.validateOnError()
	if err != nil {
		return err
	}

	if r.Query == nil {
		return nil
	}

	trimmed := r.GetTrimmedQuery()

	// a query that opens like a JsonLogic object and does not decode is rejected, as it is for the rules of the flag
	// (it is not a valid Rules query either); the default rule ignores its query
	if !isDefault && strings.HasPrefix(strings.TrimLeft(trimmed, jsonWhitespace), "{") &&
		queryFormatOf(trimmed) != JSONLogicQueryFormat {
		return errInvalidJsonlogicQuery
	}

	if !hasSegmentTerm(trimmed) {
		return nil
	}

	if isDefault {
		return errSegmentInDefaultRule
	}

	if queryFormatOf(trimmed) == JSONLogicQueryFormat {
		return newCompiledQuery(r.GetQuery()).segErr
	}

	_, err = svc.nikunjy.get(trimmed)
	if err != nil {
		return fmt.Errorf("parsing the query: %w", err)
	}

	return nil
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
				terms = append(terms, segment.Term{Name: term.Name, Path: slices.Clone(term.Path)})
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

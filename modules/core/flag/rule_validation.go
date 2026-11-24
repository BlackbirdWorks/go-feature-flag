package flag

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/diegoholiveira/jsonlogic/v3"

	"github.com/blackbirdworks/semaphore/rules/parser"
)

var (
	errRolloutInitialNotLowerThanEnd = errors.New(
		"invalid progressive rollout, initial percentage should be lower than end percentage",
	)
	errImpossibleToReturnValue          = errors.New("impossible to return value")
	errInvalidPercentagesShouldNot      = errors.New("invalid percentages: should not be empty")
	errInvalidPercentageVariation       = errors.New("invalid percentage: variation")
	errInvalidPercentagesShouldNotBe    = errors.New("invalid percentages: should not be equal to 0")
	errInvalidPercentagesTheSum         = errors.New("invalid percentages: the sum")
	errInvalidPercentage                = errors.New("invalid percentage")
	errInvalidProgressiveRolloutInitial = errors.New(
		"invalid progressive rollout, initial and end steps are required",
	)
	errInvalidProgressiveRolloutEnd              = errors.New("invalid progressive rollout, end variation")
	errInvalidProgressiveRolloutInitialVariation = errors.New("invalid progressive rollout, initial variation")
	errInvalidVariation                          = errors.New("invalid variation")
	errEachTargetingShouldHave                   = errors.New("each targeting should have a query")
	errInvalidJsonlogicQuery                     = errors.New("invalid jsonlogic query")
)

// IsValid is checking if the rule is valid.
func (r *Rule) IsValid(svc *Service, defaultRule bool, variations map[string]*any) error {
	_, err := r.validate(svc, defaultRule, variations, nil)

	return err
}

// validate is IsValid. It also returns the evaluator that validated the nikunjy query of the
// rule, so that the caller does not have to look the query up again. It is nil if the rule has
// no nikunjy query to validate.
//
// known is the evaluator of a nikunjy query that was validated already: the query is not
// validated again.
func (r *Rule) validate(
	svc *Service, defaultRule bool, variations map[string]*any, known *parser.Evaluator,
) (*parser.Evaluator, error) {
	if !defaultRule && r.IsDisable() {
		//nolint:nilnil // a nil evaluator means the rule has no nikunjy query to validate (documented on validate)
		return nil, nil
	}

	if r.Percentages == nil && r.ProgressiveRollout == nil && r.VariationResult == nil {
		return nil, errImpossibleToReturnValue
	}

	ev := known
	if ev == nil {
		var err error
		if ev, err = r.validateQuery(svc, defaultRule); err != nil {
			return nil, err
		}
	}

	if err := r.validatePercentages(variations); err != nil {
		return nil, err
	}

	if err := r.validateProgressiveRollout(variations); err != nil {
		return nil, err
	}

	if err := r.validateVariationResult(variations); err != nil {
		return nil, err
	}

	return ev, nil
}

// validatePercentages validates the percentage configuration of the rule.
// It checks that percentages are not empty, none of them is negative or not a finite number,
// the sum is not zero and does not overflow once scaled, and all referenced variations exist in the provided
// variations map.
func (r *Rule) validatePercentages(variations map[string]*any) error {
	if r.Percentages == nil {
		return nil
	}

	percentages := r.GetPercentages()
	if len(percentages) == 0 {
		return errInvalidPercentagesShouldNot
	}

	count := float64(0)
	for k, p := range percentages {
		if err := r.validateSinglePercentage(p, fmt.Sprintf("variation %s", k)); err != nil {
			return err
		}
		count += p
		if _, ok := variations[k]; !ok {
			return fmt.Errorf("%w %s does not exist", errInvalidPercentageVariation, k)
		}
	}

	if count == 0 {
		return errInvalidPercentagesShouldNotBe
	}

	// the buckets are scaled by PercentageMultiplier and stored as uint32.
	if count*PercentageMultiplier > math.MaxUint32 {
		return fmt.Errorf("%w (%v) is too large", errInvalidPercentagesTheSum, count)
	}

	return nil
}

// validateSinglePercentage validates a single percentage.
func (r *Rule) validateSinglePercentage(p float64, errCtx string) error {
	if p < 0 || math.IsNaN(p) || math.IsInf(p, 0) {
		return fmt.Errorf("%w: %s has a negative or non-finite percentage (%v)", errInvalidPercentage, errCtx, p)
	}

	return nil
}

// validateProgressiveRollout validates the progressive rollout configuration of the rule.
// It checks that the initial percentage is lower than the end percentage, and that
// both the initial and end variations exist in the provided variations map.
func (r *Rule) validateProgressiveRollout(variations map[string]*any) error {
	if r.ProgressiveRollout == nil {
		return nil
	}

	progRollout := r.GetProgressiveRollout()
	if progRollout.Initial == nil || progRollout.End == nil {
		return errInvalidProgressiveRolloutInitial
	}

	initialPct := progRollout.Initial.getPercentage()
	if err := r.validateSinglePercentage(initialPct, "initial step percentage"); err != nil {
		return err
	}

	endPct := progRollout.End.getPercentage()
	if err := r.validateSinglePercentage(endPct, "end step percentage"); err != nil {
		return err
	}

	if endPct < initialPct {
		return fmt.Errorf("%w: %v/%v", errRolloutInitialNotLowerThanEnd, initialPct, endPct)
	}

	endVar := progRollout.End.getVariation()
	if _, ok := variations[endVar]; !ok {
		return fmt.Errorf("%w %s does not exist", errInvalidProgressiveRolloutEnd,
			endVar,
		)
	}

	initialVar := progRollout.Initial.getVariation()
	if _, ok := variations[initialVar]; !ok {
		return fmt.Errorf("%w %s does not exist", errInvalidProgressiveRolloutInitialVariation,
			initialVar,
		)
	}

	return nil
}

// validateVariationResult validates the simple variation result configuration of the rule.
// It checks that the variation result exists in the provided variations map.
// This validation only applies when the rule uses a simple variation result
// (not percentages or progressive rollout).
func (r *Rule) validateVariationResult(variations map[string]*any) error {
	if r.Percentages != nil || r.ProgressiveRollout != nil || r.VariationResult == nil {
		return nil
	}

	if _, ok := variations[r.GetVariationResult()]; !ok {
		return fmt.Errorf("%w: %s does not exist", errInvalidVariation, r.GetVariationResult())
	}

	return nil
}

// isQueryValid validates the query configuration of the rule.
// It checks that the query is not empty and that the query format is valid.
// The query format can be either JSONLogic or Nikunjy.
func (r *Rule) isQueryValid(svc *Service, defaultRule bool) error {
	_, err := r.validateQuery(svc, defaultRule)

	return err
}

// validateQuery is isQueryValid. It also returns the evaluator of a nikunjy query.
func (r *Rule) validateQuery(svc *Service, defaultRule bool) (*parser.Evaluator, error) {
	if defaultRule {
		return nil, nil //nolint:nilnil // a nil evaluator means there is no nikunjy query to validate (default rule)
	}

	if r.Query == nil {
		return nil, errEachTargetingShouldHave
	}

	// Validate the query with the parser
	switch r.GetQueryFormat() {
	case JSONLogicQueryFormat:
		if !jsonlogic.IsValid(strings.NewReader(r.GetQuery())) {
			return nil, errInvalidJsonlogicQuery
		}

		return nil, nil //nolint:nilnil // a nil evaluator means the query is JSONLogic, not nikunjy
	default:
		return svc.nikunjy.validateEvaluator(r.GetTrimmedQuery())
	}
}

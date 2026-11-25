package rules

import (
	"fmt"

	"github.com/blackbirdworks/semaphore/rules/parser"
)

func Evaluate(rule string, items map[string]any) (bool, error) {
	evaluator, err := parser.NewEvaluator(rule)
	if err != nil {
		return false, fmt.Errorf("parsing the rule: %w", err)
	}

	res, err := evaluator.Process(items)
	if err != nil {
		return false, fmt.Errorf("evaluating the rule: %w", err)
	}

	return res, nil
}

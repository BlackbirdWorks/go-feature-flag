package rules

import "github.com/blackbirdworks/semaphore/rules/parser"

func Evaluate(rule string, items map[string]any) (bool, error) {
	evaluator, err := parser.NewEvaluator(rule)
	if err != nil {
		//nolint:wrapcheck // preserves rule parser error on evaluation path
		return false, err
	}

	//nolint:wrapcheck // preserves rule evaluation error on evaluation path
	return evaluator.Process(items)
}

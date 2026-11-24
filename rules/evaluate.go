package rules

import "github.com/blackbirdworks/semaphore/rules/parser"

func Evaluate(rule string, items map[string]any) (bool, error) {
	ev, err := parser.NewEvaluator(rule)
	if err != nil {
		return false, err
	}

	return ev.Process(items)
}

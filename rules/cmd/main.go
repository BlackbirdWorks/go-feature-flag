// Command cmd evaluates the rule in rules.txt against the JSON object in test.json.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/blackbirdworks/semaphore/rules/parser"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	jsonFile, err := os.ReadFile("test.json")
	if err != nil {
		return fmt.Errorf("error reading obj from file: %w", err)
	}
	rulesBytes, err := os.ReadFile("rules.txt")
	if err != nil {
		return fmt.Errorf("error reading rule from file: %w", err)
	}
	rulesString := string(rulesBytes)
	var info map[string]any
	if err := json.Unmarshal(jsonFile, &info); err != nil {
		return fmt.Errorf("error decoding obj from file: %w", err)
	}
	ev, err := parser.NewEvaluator(rulesString)
	if err != nil {
		return fmt.Errorf("error making evaluator from the rule %v: %w", rulesString, err)
	}
	ans, err := ev.Process(info)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, `
		Rule: %v
		Object: %v
		Answer: %v
	`+"\n", rulesString, info, ans)
	if err := ev.LastDebugErr(); err != nil {
		fmt.Fprintln(os.Stdout, "Last debug error", err)
	}

	return nil
}

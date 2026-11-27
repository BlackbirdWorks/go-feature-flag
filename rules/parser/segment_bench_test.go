package parser_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/rules/parser"
)

func BenchmarkProcessSegments(b *testing.B) {
	items := map[string]any{attrTargetingKey: "alice", attrEmail: "a@b.c"}

	benches := []struct {
		name   string
		rule   string
		states []parser.SegmentState
	}{
		{
			name:   "one term",
			rule:   `targetingKey in segment("beta")`,
			states: []parser.SegmentState{parser.SegmentMember},
		},
		{
			name:   "include and exclude",
			rule:   `targetingKey in segment("beta") and not (email in segment("churned"))`,
			states: []parser.SegmentState{parser.SegmentMember, parser.SegmentNotMember},
		},
	}

	for _, bench := range benches {
		b.Run(bench.name, func(b *testing.B) {
			evaluator, err := parser.NewEvaluator(bench.rule)
			require.NoError(b, err)

			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				_, _ = evaluator.ProcessSegments(items, bench.states)
			}
		})
	}
}

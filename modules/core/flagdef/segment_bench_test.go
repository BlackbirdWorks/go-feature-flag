package flagdef_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

func BenchmarkSegmentRuleEvaluation(b *testing.B) {
	provider := segment.NewMemoryProvider(map[string][]string{"beta": {keyAlice}, segChurned: {"bob"}})

	benches := []struct {
		name  string
		query string
		key   string
	}{
		{name: "plain nikunjy rule (baseline)", query: queryEqAlice, key: keyAlice},
		{name: "bench: nikunjy member", query: nikunjyInclude, key: keyAlice},
		{name: "bench: nikunjy not member", query: nikunjyInclude, key: "zed"},
		{
			name:  "nikunjy include and exclude",
			query: `targetingKey in segment("beta") and not (targetingKey in segment("churned"))`,
			key:   keyAlice,
		},
		{name: "plain jsonlogic rule (baseline)", query: `{"==": [{"var": "targetingKey"}, "alice"]}`, key: keyAlice},
		{name: "jsonlogic member", query: jsonInclude, key: keyAlice},
	}

	for _, bench := range benches {
		b.Run(bench.name, func(b *testing.B) {
			svc := flagdef.NewService()
			flag := onOffFlag(queryRule("r", bench.query))
			require.NoError(b, flag.IsValid(svc))

			evalCtx := ffcontext.NewEvaluationContext(bench.key)
			flagCtx := flagdef.Context{Service: svc, Segments: provider}

			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				flag.Value("f", evalCtx, flagCtx)
			}
		})
	}
}

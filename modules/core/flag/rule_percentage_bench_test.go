package flag_test

import (
	"testing"

	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/flag"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

func BenchmarkRuleEvaluate_Percentage(b *testing.B) {
	svc := flag.NewService()

	const key = "3f1c1a52-8a5e-4c1e-9d0a-2b7f6c2e9a10"

	tests := []struct {
		percentages map[string]float64
		name        string
	}{
		{
			name:        "2 variations",
			percentages: map[string]float64{varAName: 50, varBName: 50},
		},
		{
			name: "5 variations",
			percentages: map[string]float64{
				varAName: 20, varBName: 20, "varC": 20, "varD": 20, "varE": 20,
			},
		},
	}
	ctx := ffcontext.NewEvaluationContext(key)

	for _, tt := range tests {
		rule := flag.Rule{Percentages: &tt.percentages}
		b.Run(tt.name, func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				_, _ = rule.Evaluate(svc, key, ctx, fixtures.FlagKeyMy, true)
			}
		})
	}
}

func BenchmarkRuleEvaluate_JSONLogic(b *testing.B) {
	svc := flag.NewService()
	rule := flag.Rule{
		VariationResult: new("on"),
		Query: new(
			`{"and":[{"==":[{"var":"language"},"ar"]},{">":[{"var":"clubsTimeSpent"},600]}]}`,
		),
	}
	ctx := ffcontext.NewEvaluationContextBuilder(fixtures.UserKeyOne).
		AddCustom(language, "ar").
		AddCustom("clubsTimeSpent", 1500).
		Build()

	b.ReportAllocs()

	for b.Loop() {
		_, _ = rule.Evaluate(svc, fixtures.UserKeyOne, ctx, fixtures.FlagKeyMy, false)
	}
}

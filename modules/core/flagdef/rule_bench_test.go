package flagdef_test

import (
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

const (
	groupE   = "group-e"
	language = "language"
)

// BenchmarkRuleEvaluate_NikunjyComplex measures the per-call cost of evaluating a
// nikunjy targeting query. Before the parser cache, every call re-parses the query
// via ANTLR; after the cache, parsing happens once per distinct query string.
func BenchmarkRuleEvaluate_NikunjyComplex(b *testing.B) {
	svc := flagdef.NewService()
	rule := flagdef.Rule{
		Name:            new("complex"),
		VariationResult: new("on"),
		Query: new(
			`language eq "ar" and isNewUser eq true and clubsTimeSpent gt 600 ` +
				`and clubsTimeSpent le 3600 and concurrencyLocked eq true and segment eq 0`,
		),
	}
	ctx := ffcontext.NewEvaluationContextBuilder(fixtures.UserKeyOne).
		AddCustom(language, "ar").
		AddCustom("isNewUser", true).
		AddCustom("clubsTimeSpent", 1500).
		AddCustom("concurrencyLocked", true).
		AddCustom("segment", 0).
		Build()

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		_, _ = rule.Evaluate(svc, fixtures.UserKeyOne, ctx, fixtures.FlagKeyTest, false)
	}
}

// BenchmarkRuleEvaluate_NikunjyColdAfterGC evaluates many distinct queries, each
// once per round, with garbage collections between rounds. It models a large flag
// configuration where most flags are evaluated less often than the GC runs.
func BenchmarkRuleEvaluate_NikunjyColdAfterGC(b *testing.B) {
	svc := flagdef.NewService()

	const queries = 1000

	rules := make([]flagdef.Rule, queries)
	for i := range rules {
		rules[i] = flagdef.Rule{
			Name:            new(fmt.Sprintf("cold-%d", i)),
			VariationResult: new("on"),
			Query:           new(fmt.Sprintf(`group eq "group-%d"`, i)),
		}
		_, _ = rules[i].Evaluate(
			svc,
			fixtures.UserKeyOne,
			ffcontext.NewEvaluationContext(fixtures.UserKeyOne),
			"warm-up",
			false,
		)
	}

	ctx := ffcontext.NewEvaluationContextBuilder(fixtures.UserKeyOne).
		AddCustom(fixtures.AttrGroup, "group-0").
		Build()

	b.ReportAllocs()
	b.ResetTimer()

	for i := range b.N {
		if i%queries == 0 {
			b.StopTimer()
			runtime.GC()
			runtime.GC()
			b.StartTimer()
		}

		_, _ = rules[i%queries].Evaluate(svc, fixtures.UserKeyOne, ctx, fixtures.FlagKeyTest, false)
	}
}

// BenchmarkRuleEvaluate_NikunjySimple is a single-predicate variant for comparison.
func BenchmarkRuleEvaluate_NikunjySimple(b *testing.B) {
	svc := flagdef.NewService()
	rule := flagdef.Rule{
		Name:            new("simple"),
		VariationResult: new("on"),
		Query:           new(`language eq "ar"`),
	}
	ctx := ffcontext.NewEvaluationContextBuilder(fixtures.UserKeyOne).
		AddCustom(language, "ar").
		Build()

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		_, _ = rule.Evaluate(svc, fixtures.UserKeyOne, ctx, fixtures.FlagKeyTest, false)
	}
}

// BenchmarkRuleEvaluate_NikunjyLargeIn evaluates a rule that targets 400 UUID-length
// user IDs, the shape of a flag that lists individual targets, with a context
// that is not in the list.
func BenchmarkRuleEvaluate_NikunjyLargeIn(b *testing.B) {
	svc := flagdef.NewService()

	users := make([]string, 400)
	for i := range users {
		users[i] = fmt.Sprintf(`"%08d-0000-4000-8000-000000000000"`, i)
	}

	rule := flagdef.Rule{
		Name:            new("large-in"),
		VariationResult: new("on"),
		Query:           new(`userId in [` + strings.Join(users, ", ") + `]`),
	}
	ctx := ffcontext.NewEvaluationContextBuilder(fixtures.UserKeyOne).
		AddCustom("userId", "ffffffff-0000-4000-8000-000000000000").
		Build()

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		_, _ = rule.Evaluate(svc, fixtures.UserKeyOne, ctx, fixtures.FlagKeyTest, false)
	}
}

// BenchmarkInternalFlagValue_MultiRule evaluates a flag whose targeting rules all
// carry a query, the last one matching, so every rule reads the evaluation context.
func BenchmarkInternalFlagValue_MultiRule(b *testing.B) {
	svc := flagdef.NewService()

	rules := make([]flagdef.Rule, 0, 5)
	for _, group := range []string{"group-a", "group-b", "group-c", "group-d", groupE} {
		rules = append(rules, flagdef.Rule{
			Query:           new(`group eq "` + group + `" and plan eq "pro"`),
			VariationResult: new("on"),
		})
	}

	flg := flagdef.InternalFlag{
		Variations: &map[string]*any{
			"on":                  new(any(true)),
			fixtures.VariationOff: new(any(false)),
		},
		Rules:       &rules,
		DefaultRule: &flagdef.Rule{VariationResult: new(fixtures.VariationOff)},
	}
	ctx := ffcontext.NewEvaluationContextBuilder(fixtures.UserKeyOne).
		AddCustom(fixtures.AttrGroup, groupE).
		AddCustom(fixtures.AttrPlan, fixtures.PlanPro).
		AddCustom("region", "eu").
		AddCustom("beta", true).
		Build()
	flagCtx := flagdef.Context{DefaultSdkValue: false, Service: svc}

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		_, _ = flg.Value("multi-rule", ctx, flagCtx)
	}
}

// BenchmarkInternalFlagValue_MultiRuleJSONLogic is BenchmarkInternalFlagValue_MultiRule
// with JsonLogic queries, which read the evaluation context as JSON.
func BenchmarkInternalFlagValue_MultiRuleJSONLogic(b *testing.B) {
	svc := flagdef.NewService()

	rules := make([]flagdef.Rule, 0, 5)
	for _, group := range []string{"group-a", "group-b", "group-c", "group-d", groupE} {
		rules = append(rules, flagdef.Rule{
			Query: new(
				`{"and": [{"==": [{"var": "group"}, "` + group + `"]}, {"==": [{"var": "plan"}, "pro"]}]}`,
			),
			VariationResult: new("on"),
		})
	}

	flg := flagdef.InternalFlag{
		Variations: &map[string]*any{
			"on":                  new(any(true)),
			fixtures.VariationOff: new(any(false)),
		},
		Rules:       &rules,
		DefaultRule: &flagdef.Rule{VariationResult: new(fixtures.VariationOff)},
	}
	ctx := ffcontext.NewEvaluationContextBuilder(fixtures.UserKeyOne).
		AddCustom(fixtures.AttrGroup, groupE).
		AddCustom(fixtures.AttrPlan, fixtures.PlanPro).
		AddCustom("region", "eu").
		AddCustom("beta", true).
		Build()
	flagCtx := flagdef.Context{DefaultSdkValue: false, Service: svc}

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		_, _ = flg.Value("multi-rule-jsonlogic", ctx, flagCtx)
	}
}

// BenchmarkRuleEvaluate_NikunjySimpleParallel evaluates one query from every P at
// once, the case where concurrent evaluations compete for the cached evaluators.
func BenchmarkRuleEvaluate_NikunjySimpleParallel(b *testing.B) {
	svc := flagdef.NewService()
	rule := flagdef.Rule{
		Name:            new("simple-parallel"),
		VariationResult: new("on"),
		Query:           new(`language eq "ar"`),
	}
	ctx := ffcontext.NewEvaluationContextBuilder(fixtures.UserKeyOne).
		AddCustom(language, "ar").
		Build()

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = rule.Evaluate(svc, fixtures.UserKeyOne, ctx, fixtures.FlagKeyTest, false)
		}
	})
}

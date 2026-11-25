package flag_test

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/flag"
	"github.com/blackbirdworks/semaphore/modules/core/testhelpers/clocktest"
)

const (
	flag2       = "flag"
	keyEqUser1  = `key eq "user-1" and plan eq "pro"`
	off2        = "off"
	plan2       = "plan"
	pro         = "pro"
	someoneElse = "someone-else"
	targeting2  = "targeting"
)

// largeInQuery returns `key in ["user-0000000", ...]` with 300000 ids, a ~4.5 MB query.
func largeInQuery() string {
	return largeInQueryVariant(300000, "")
}

// largeInQueryVariant is largeInQuery with a prefix in its ids, so that variants differ.
func largeInQueryVariant(n int, prefix string) string {
	var builder strings.Builder
	builder.Grow(n*16 + 16)
	builder.WriteString(`key in [`)

	for i := range n {
		if i > 0 {
			builder.WriteString(", ")
		}

		fmt.Fprintf(&builder, `"%suser-%07d"`, prefix, i)
	}

	builder.WriteString(`]`)

	return builder.String()
}

// newValidatedFlag builds a flag with one targeting rule and validates it, as the flag
// cache does when it loads a configuration.
func newValidatedFlag(b *testing.B, svc *flag.Service, query string) *flag.InternalFlag {
	b.Helper()

	flg := &flag.InternalFlag{
		Variations: &map[string]*any{"on": new(any(true)), off2: new(any(false))},
		Rules: &[]flag.Rule{{
			Name:            new(targeting2),
			Query:           new(query),
			VariationResult: new("on"),
		}},
		DefaultRule: &flag.Rule{VariationResult: new(off2)},
	}
	require.NoError(b, flg.IsValid(svc))

	return flg
}

// sharedBy100Flags returns 100 validated flags that carry the same 300000 ids list, each with
// its own copy of the text, as every flag parsed from a configuration file does.
func sharedBy100Flags(b *testing.B, svc *flag.Service) []*flag.InternalFlag {
	b.Helper()

	query := largeInQuery()

	flags := make([]*flag.InternalFlag, 100)
	for i := range flags {
		flags[i] = newValidatedFlag(b, svc, strings.Clone(query))
	}

	return flags
}

func benchValue(b *testing.B, svc *flag.Service, flags []*flag.InternalFlag, key string) {
	b.Helper()

	ctx := ffcontext.NewEvaluationContext(key)

	b.ReportAllocs()
	b.ResetTimer()

	for i := range b.N {
		flags[i%len(flags)].Value(flag2, ctx, flag.Context{Service: svc})
	}
}

// BenchmarkInternalFlagValue_LargeIn300kMiss evaluates a flag whose single rule lists 300000
// ids (a ~4.5 MB query) against a context that is not in the list.
func BenchmarkInternalFlagValue_LargeIn300kMiss(b *testing.B) {
	svc := flag.NewService()
	benchValue(b, svc, []*flag.InternalFlag{newValidatedFlag(b, svc, largeInQuery())}, someoneElse)
}

// BenchmarkInternalFlagValue_LargeIn300kHit is the same with the last id of the list.
func BenchmarkInternalFlagValue_LargeIn300kHit(b *testing.B) {
	svc := flag.NewService()
	benchValue(b, svc, []*flag.InternalFlag{newValidatedFlag(b, svc, largeInQuery())}, "user-0299999")
}

// BenchmarkInternalFlagValue_LargeIn300kSharedBy100Flags evaluates 100 flags that carry
// the same 300000 ids list, round-robin. Every flag owns its copy of the query text, as
// every flag parsed from a configuration file does.
func BenchmarkInternalFlagValue_LargeIn300kSharedBy100Flags(b *testing.B) {
	svc := flag.NewService()
	benchValue(b, svc, sharedBy100Flags(b, svc), someoneElse)
}

// BenchmarkInternalFlagValue_LargeIn300kDifferentIn10Flags evaluates 10 flags that carry 10
// different 300000 ids lists, round-robin (100 lists need too much memory).
func BenchmarkInternalFlagValue_LargeIn300kDifferentIn10Flags(b *testing.B) {
	svc := flag.NewService()

	flags := make([]*flag.InternalFlag, 10)
	for i := range flags {
		flags[i] = newValidatedFlag(b, svc, largeInQueryVariant(300000, fmt.Sprintf("list%d-", i)))
	}

	benchValue(b, svc, flags, someoneElse)
}

// BenchmarkInternalFlagValue_LargeIn300kParallel evaluates 100 flags sharing a 300000 ids
// list from all the CPUs.
func BenchmarkInternalFlagValue_LargeIn300kParallel(b *testing.B) {
	svc := flag.NewService()
	flags := sharedBy100Flags(b, svc)
	ctx := ffcontext.NewEvaluationContext(someoneElse)

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			flags[i%len(flags)].Value(flag2, ctx, flag.Context{Service: svc})
			i++
		}
	})
}

// benchReload is the time to load a configuration of 100 flags that carry the same list of ids
// while the previous configuration is still loaded.
func benchReload(b *testing.B, ids int) {
	b.Helper()

	svc := flag.NewService()
	query := largeInQueryVariant(ids, "")
	load := func() []*flag.InternalFlag {
		flags := make([]*flag.InternalFlag, 100)
		for i := range flags {
			flags[i] = newValidatedFlag(b, svc, strings.Clone(query))
		}

		return flags
	}
	previous := load()

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		// The loader builds the flags from the file before validating them.
		b.StopTimer()

		next := make([]*flag.InternalFlag, 100)
		for j := range next {
			next[j] = &flag.InternalFlag{
				Variations: &map[string]*any{"on": new(any(true)), off2: new(any(false))},
				Rules: &[]flag.Rule{{
					Name:            new(targeting2),
					Query:           new(strings.Clone(query)),
					VariationResult: new("on"),
				}},
				DefaultRule: &flag.Rule{VariationResult: new(off2)},
			}
		}

		b.StartTimer()

		for _, f := range next {
			require.NoError(b, f.IsValid(svc))
		}

		previous = next
	}

	_ = previous
}

func BenchmarkInternalFlagIsValid_Reload100FlagsSharingLargeIn(b *testing.B) { benchReload(b, 300000) }
func BenchmarkInternalFlagIsValid_Reload100FlagsSharing20kIn(b *testing.B)   { benchReload(b, 20000) }

// BenchmarkInternalFlagValue_SmallQuery is the baseline that a query lookup change must
// not regress.
func BenchmarkInternalFlagValue_SmallQuery(b *testing.B) {
	svc := flag.NewService()
	flg := newValidatedFlag(b, svc, keyEqUser1)
	ctx := ffcontext.NewEvaluationContextBuilder(user1).AddCustom(plan2, pro).Build()

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		flg.Value(flag2, ctx, flag.Context{Service: svc})
	}
}

// BenchmarkInternalFlagValue_LargeIn300kGCEvery10 is BenchmarkInternalFlagValue_LargeIn300kMiss
// with two garbage collections, which drop the evaluators held by a [sync.Pool], every 10
// evaluations. The collections are not timed, what follows them is.
func BenchmarkInternalFlagValue_LargeIn300kGCEvery10(b *testing.B) {
	svc := flag.NewService()
	flg := newValidatedFlag(b, svc, largeInQuery())
	ctx := ffcontext.NewEvaluationContext(someoneElse)

	b.ReportAllocs()
	b.ResetTimer()

	for i := range b.N {
		if i%10 == 0 {
			b.StopTimer()
			runtime.GC()
			runtime.GC()
			b.StartTimer()
		}

		flg.Value(flag2, ctx, flag.Context{Service: svc})
	}
}

func newSmallFlag(i int, query string) *flag.InternalFlag {
	flg := &flag.InternalFlag{
		Variations:  &map[string]*any{"on": new(any(true)), off2: new(any(false))},
		DefaultRule: &flag.Rule{VariationResult: new(off2)},
	}
	if query != "" {
		flg.Rules = &[]flag.Rule{{
			Name:            new(fmt.Sprintf("rule-%d", i)),
			Query:           new(query),
			VariationResult: new("on"),
		}}
	}

	return flg
}

func smallQueries(n int) []string {
	queries := make([]string, n)
	for i := range queries {
		queries[i] = fmt.Sprintf(`key eq "user-%d" and plan eq "pro"`, i)
	}

	return queries
}

// benchLoad measures IsValid of count flags that each carry a distinct short query, the shape of a
// typical configuration file. The flags are built outside the measure.
func benchLoad(b *testing.B, count int) {
	b.Helper()

	svc := flag.NewService()
	queries := smallQueries(count)

	var keep []*flag.InternalFlag

	b.ReportAllocs()

	for range b.N {
		b.StopTimer()

		flags := make([]*flag.InternalFlag, count)
		for j := range flags {
			flags[j] = newSmallFlag(j, strings.Clone(queries[j]))
		}

		b.StartTimer()

		for _, flg := range flags {
			require.NoError(b, flg.IsValid(svc))
		}

		keep = flags
	}

	_ = keep
}

func BenchmarkInternalFlagIsValid_Load1000SmallFlags(b *testing.B)  { benchLoad(b, 1000) }
func BenchmarkInternalFlagIsValid_Load10000SmallFlags(b *testing.B) { benchLoad(b, 10000) }

// BenchmarkInternalFlagValue_Small10000DistinctFlags evaluates 10000 validated flags with
// distinct short queries, round-robin.
func BenchmarkInternalFlagValue_Small10000DistinctFlags(b *testing.B) {
	svc := flag.NewService()
	queries := smallQueries(10000)

	flags := make([]*flag.InternalFlag, len(queries))
	for i := range flags {
		flags[i] = newSmallFlag(i, queries[i])
		require.NoError(b, flags[i].IsValid(svc))
	}

	ctx := ffcontext.NewEvaluationContextBuilder(user1).AddCustom(plan2, pro).Build()

	b.ReportAllocs()
	b.ResetTimer()

	for i := range b.N {
		flags[i%len(flags)].Value(flag2, ctx, flag.Context{Service: svc})
	}
}

func BenchmarkInternalFlagValue_SmallQueryParallel(b *testing.B) {
	svc := flag.NewService()
	flg := newSmallFlag(0, keyEqUser1)
	require.NoError(b, flg.IsValid(svc))

	ctx := ffcontext.NewEvaluationContextBuilder(user1).AddCustom(plan2, pro).Build()

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			flg.Value(flag2, ctx, flag.Context{Service: svc})
		}
	})
}

func BenchmarkInternalFlagValue_NoQuery(b *testing.B) {
	svc := flag.NewService()
	flg := newSmallFlag(0, "")
	require.NoError(b, flg.IsValid(svc))

	ctx := ffcontext.NewEvaluationContext(user1)

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		flg.Value(flag2, ctx, flag.Context{Service: svc})
	}
}

func BenchmarkInternalFlagValue_JSONLogic(b *testing.B) {
	svc := flag.NewService()
	flg := newSmallFlag(0, `{"and": [{"==": [{"var": "key"}, "user-1"]}, {"==": [{"var": "plan"}, "pro"]}]}`)
	require.NoError(b, flg.IsValid(svc))

	ctx := ffcontext.NewEvaluationContextBuilder(user1).AddCustom(plan2, pro).Build()

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		flg.Value(flag2, ctx, flag.Context{Service: svc})
	}
}

// BenchmarkRuleEvaluate_NotValidated evaluates a rule that was never validated, as a rule built
// in code is.
func BenchmarkRuleEvaluate_NotValidated(b *testing.B) {
	svc := flag.NewService()
	r := flag.Rule{
		Query:           new(keyEqUser1),
		VariationResult: new("on"),
	}
	ctx := ffcontext.NewEvaluationContextBuilder(user1).AddCustom(plan2, pro).Build()

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		_, _ = r.Evaluate(svc, user1, ctx, flag2, false)
	}
}

// BenchmarkInternalFlagValue_Scheduled evaluates a flag with a scheduled rollout, which copies the
// flag through JSON on every evaluation.
func BenchmarkInternalFlagValue_Scheduled(b *testing.B) {
	svc := flag.NewService()
	flg := newSmallFlag(0, keyEqUser1)
	past := clocktest.Instant().Add(-time.Hour)
	flg.Scheduled = &[]flag.ScheduledStep{{
		Rules: &[]flag.Rule{{
			Name:  new("rule-0"),
			Query: new(keyEqUser1),
		}},
		Date: &past,
	}}
	require.NoError(b, flg.IsValid(svc))

	ctx := ffcontext.NewEvaluationContextBuilder(user1).AddCustom(plan2, pro).Build()

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		flg.Value(flag2, ctx, flag.Context{Service: svc})
	}
}

func heapInUse() float64 {
	runtime.GC()
	runtime.GC()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	return float64(m.HeapAlloc)
}

// BenchmarkHeap_AfterLoadAndReloads reports the heap retained by a loaded configuration of 1000
// flags with distinct short queries, each evaluated once (heap-load-B), and after 30 reloads that
// change every query (heap-reloads-B).
func BenchmarkHeap_AfterLoadAndReloads(b *testing.B) {
	svc := flag.NewService()
	ctx := ffcontext.NewEvaluationContextBuilder(user1).AddCustom(plan2, pro).Build()

	for range b.N {
		base := heapInUse()
		load := func(generation int) []*flag.InternalFlag {
			flags := make([]*flag.InternalFlag, 1000)
			for j := range flags {
				flags[j] = newSmallFlag(j, fmt.Sprintf(`key eq "user-%d-%d" and plan eq "pro"`, generation, j))
				require.NoError(b, flags[j].IsValid(svc))
				flags[j].Value(flag2, ctx, flag.Context{Service: svc})
			}

			return flags
		}
		current := load(0)
		afterLoad := heapInUse() - base

		for g := 1; g <= 30; g++ {
			current = load(g)
		}

		afterReloads := heapInUse() - base

		b.ReportMetric(afterLoad, "heap-load-B")
		b.ReportMetric(afterReloads, "heap-reloads-B")
		runtime.KeepAlive(current)
	}
}

// BenchmarkHeap_LargeListReloads reports the heap retained after 8 reloads of 5 flags that carry
// a 20000 ids list, each reload with another list, every flag evaluated once (heap-B).
func BenchmarkHeap_LargeListReloads(b *testing.B) {
	svc := flag.NewService()
	ctx := ffcontext.NewEvaluationContext(someoneElse)

	for range b.N {
		base := heapInUse()

		var current []*flag.InternalFlag

		for g := range 8 {
			query := largeInQueryVariant(20000, fmt.Sprintf("gen%d-", g))

			current = make([]*flag.InternalFlag, 5)
			for j := range current {
				current[j] = newValidatedFlag(b, svc, strings.Clone(query))
				current[j].Value(flag2, ctx, flag.Context{Service: svc})
			}
		}

		b.ReportMetric(heapInUse()-base, "heap-B")
		runtime.KeepAlive(current)
	}
}

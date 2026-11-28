package flagdef_test

import (
	"strconv"
	"strings"
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
		{name: "bench: nikunjy not member", query: nikunjyInclude, key: keyZed},
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

// BenchmarkStaticSegmentRule compares the rule over a static segment of 100 members, answered from the catalog of the
// evaluation, with the same segment asked of a memory provider (what a static segment cost before) and with a rule
// over an `in [...]` list of the same members.
func BenchmarkStaticSegmentRule(b *testing.B) {
	const members = 100

	list := make([]string, 0, members)
	quoted := make([]string, 0, members)

	for i := range members {
		id := "m" + strconv.Itoa(i)
		list = append(list, id)
		quoted = append(quoted, strconv.Quote(id))
	}

	catalog, err := segment.NewCatalogWithSets(
		map[string]*segment.MemberSet{segBeta: segment.NewMemberSet(list)},
		segment.Definition{Name: segBeta, Type: segment.TypeFile},
	)
	require.NoError(b, err)

	provider := segment.NewMemoryProvider(map[string][]string{segBeta: list})
	inList := `targetingKey in [` + strings.Join(quoted, ", ") + `]`

	benches := []struct {
		catalog  *segment.Catalog
		provider segment.Provider
		name     string
		query    string
		key      string
	}{
		{name: "in list, member", query: inList, key: keyM99},
		{name: "in list, not member", query: inList, key: keyZed},
		{name: "memory provider, member", query: nikunjyInclude, key: keyM99, provider: provider},
		{name: "memory provider, not member", query: nikunjyInclude, key: keyZed, provider: provider},
		{name: "static catalog, member", query: nikunjyInclude, key: keyM99, catalog: catalog},
		{name: "static catalog, not member", query: nikunjyInclude, key: keyZed, catalog: catalog},
	}

	for _, bench := range benches {
		b.Run(bench.name, func(b *testing.B) {
			svc := flagdef.NewService()
			flag := onOffFlag(queryRule("r", bench.query))
			require.NoError(b, flag.IsValid(svc))

			evalCtx := ffcontext.NewEvaluationContext(bench.key)
			flagCtx := flagdef.Context{Service: svc, Segments: bench.provider, SegmentCatalog: bench.catalog}

			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				flag.Value("f", evalCtx, flagCtx)
			}
		})
	}
}

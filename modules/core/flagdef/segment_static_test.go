package flagdef_test

import (
	"context"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

// recordsCalls is a provider that records the segments that it is asked and answers NotMember.
type recordsCalls struct {
	got *[]string
}

func (p recordsCalls) Contains(_ context.Context, lookups []segment.Lookup) error {
	for i := range lookups {
		*p.got = append(*p.got, lookups[i].Segment)
		lookups[i].State = segment.NotMember
	}

	return nil
}

func staticCatalog(t *testing.T) *segment.Catalog {
	t.Helper()

	catalog, err := segment.NewCatalogWithSets(
		map[string]*segment.MemberSet{segBeta: segment.NewMemberSet([]string{keyAlice})},
		segment.Definition{Name: segBeta, Type: segment.TypeFile},
		segment.Definition{Name: segChurned, Type: segment.TypeRedis},
	)
	require.NoError(t, err)

	return catalog
}

// A static segment of the catalog of the evaluation is answered from memory: the provider is not called for it,
// and a dynamic segment of the same rule still goes to the provider.
func TestStaticSegmentsAreAnsweredFromMemory(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		query       string
		key         string
		wantVariant string
		wantAsked   []string
		wantStatus  segment.Status
	}{
		{
			name:        nameMember,
			query:       nikunjyInclude,
			key:         keyAlice,
			wantVariant: variantOn,
			wantStatus:  segment.StatusHealthy,
		},
		{
			name:        "not a member",
			query:       nikunjyInclude,
			key:         keyBob,
			wantVariant: variantOff,
			wantStatus:  segment.StatusHealthy,
		},
		{
			name: "negated, not a member", query: nikunjyExclude, key: keyBob,
			wantVariant: variantOn, wantStatus: segment.StatusHealthy,
		},
		{
			name:        "static and dynamic: only the dynamic one is asked",
			query:       `targetingKey in segment("beta") or targetingKey in segment("churned")`,
			key:         keyBob,
			wantVariant: variantOff,
			wantStatus:  segment.StatusHealthy,
			wantAsked:   []string{segChurned},
		},
		{
			name:        "static member: the dynamic term of the rule is still asked, the static answer stands",
			query:       `targetingKey in segment("beta") or targetingKey in segment("churned")`,
			key:         keyAlice,
			wantVariant: variantOn,
			wantStatus:  segment.StatusHealthy,
			wantAsked:   []string{segChurned},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var asked []string

			flag := onOffFlag(queryRule("r", tt.query))
			svc := flagdef.NewService()
			require.NoError(t, flag.IsValid(svc))

			_, details := flag.Value("f", ffcontext.NewEvaluationContext(tt.key), flagdef.Context{
				Service: svc, SegmentCatalog: staticCatalog(t), Segments: recordsCalls{got: &asked},
			})

			assert.Equal(t, tt.wantVariant, details.Variant)
			assert.Equal(t, tt.wantStatus, details.SegmentStatus)
			assert.Equal(t, tt.wantAsked, asked)
		})
	}
}

// A dynamic segment with no provider answers by the default and reports NOT_CONFIGURED.
func TestDynamicSegmentWithoutProviderIsNotConfigured(t *testing.T) {
	t.Parallel()

	flag := onOffFlag(queryRule("r", `targetingKey in segment("churned")`))
	svc := flagdef.NewService()
	require.NoError(t, flag.IsValid(svc))

	_, details := flag.Value("f", ffcontext.NewEvaluationContext(keyAlice), flagdef.Context{
		Service: svc, SegmentCatalog: staticCatalog(t),
	})

	assert.Equal(t, variantOff, details.Variant)
	assert.Equal(t, segment.StatusNotConfigured, details.SegmentStatus)
}

// A static segment costs no more allocations than the same rule over an `in [...]` list.
//
//nolint:paralleltest // AllocsPerRun counts allocations from concurrent tests across the process
func TestStaticSegmentAllocatesNoMoreThanAnInList(t *testing.T) {
	cheapest := func(query string, catalog *segment.Catalog) float64 {
		flag := onOffFlag(queryRule("r", query))
		svc := flagdef.NewService()
		require.NoError(t, flag.IsValid(svc))

		evalCtx := ffcontext.NewEvaluationContext(keyAlice)
		flagCtx := flagdef.Context{Service: svc, SegmentCatalog: catalog}
		best := math.MaxFloat64

		for range 30 {
			best = min(best, testing.AllocsPerRun(1, func() {
				_, details := flag.Value("f", evalCtx, flagCtx)
				assert.Equal(t, variantOn, details.Variant)
			}))
		}

		return best
	}

	list := cheapest(`targetingKey in ["alice"]`, nil)
	static := cheapest(nikunjyInclude, staticCatalog(t))

	assert.LessOrEqual(t, static, list, "static segment %v allocations, in-list rule %v", static, list)
}

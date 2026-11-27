package flagdef

import (
	"context"
	"runtime"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

// The validation of a flag compiles queries that may never run: it must not leave them in the Service, which keeps
// what the evaluations compile until it is closed.
func TestValidationDoesNotMemoizeQueries(t *testing.T) {
	t.Parallel()

	svc := NewService()

	rule := Rule{Name: new("r"), Query: new(`{"inSegment": [{"var": "k"}, "a"]}`), VariationResult: new("on")}
	flag := &InternalFlag{
		Variations: &map[string]*any{"on": new(any(true)), "off": new(any(false))},
		Rules:      &[]Rule{rule},
		DefaultRule: &Rule{
			Query:           new(`{"==": [{"var": "k"}, "x"]}`),
			VariationResult: new("off"),
		},
	}

	require.NoError(t, flag.IsValid(svc))
	assert.Equal(t, []string{"a"}, flag.SegmentNames(svc))
	assert.True(t, flag.UsesSegments(svc))

	count := 0

	svc.compiled.Range(func(_, _ any) bool {
		count++

		return true
	})

	assert.Equal(t, 0, count, "nothing is memoized by the validation")
}

func mapSize(m *sync.Map) int {
	size := 0

	m.Range(func(_, _ any) bool {
		size++

		return true
	})

	return size
}

func segmentFlag(queries ...string) *InternalFlag {
	rules := make([]Rule, 0, len(queries))
	for i, query := range queries {
		rules = append(rules, Rule{Name: new("r" + strconv.Itoa(i)), Query: new(query), VariationResult: new("on")})
	}

	return &InternalFlag{
		Variations:  &map[string]*any{"on": new(any(true)), "off": new(any(false))},
		Rules:       &rules,
		DefaultRule: &Rule{VariationResult: new("off")},
	}
}

type allMembers struct{}

func (allMembers) Contains(_ context.Context, lookups []segment.Lookup) error {
	for i := range lookups {
		lookups[i].State = segment.Member
	}

	return nil
}

// The first evaluation after a load compiles nothing: the compiled segment queries are built by the validation and
// kept with the rules, the Service memoizes nothing for them.
func TestFirstEvaluationAfterLoadCompilesNothing(t *testing.T) {
	t.Parallel()

	svc := NewService()
	flag := segmentFlag(`{"inSegment": [{"var": "targetingKey"}, "beta"]}`, `targetingKey in segment("beta")`)
	require.NoError(t, flag.IsValid(svc))

	assert.Equal(t, 2, svc.segmentQueries.size(), "the validation compiled both queries")
	assert.Equal(t, 0, mapSize(&svc.compiled))

	evaluators := mapSize(&svc.nikunjy.entries)

	for range 2 {
		_, details := flag.Value(
			"f",
			ffcontext.NewEvaluationContext("k"),
			Context{Service: svc, Segments: allMembers{}},
		)
		assert.Equal(t, "on", details.Variant)
	}

	assert.Equal(t, 2, svc.segmentQueries.size())
	assert.Equal(t, 0, mapSize(&svc.compiled), "nothing was memoized by the evaluations")
	assert.Equal(t, evaluators, mapSize(&svc.nikunjy.entries), "no evaluator was parsed by the evaluations")
}

// What a flag brought stays only as long as the flag: a reload that drops it drops the compiled queries.
func TestCompiledSegmentQueriesLiveWithTheirFlag(t *testing.T) {
	t.Parallel()

	svc := NewService()

	load := func(query string) {
		require.NoError(t, segmentFlag(query).IsValid(svc)) // the flag is not kept
	}

	load(`{"inSegment": [{"var": "targetingKey"}, "beta"]}`)

	kept := segmentFlag(`targetingKey in segment("beta")`)
	require.NoError(t, kept.IsValid(svc))

	gcUntil(t, func() bool { return svc.segmentQueries.size() == 1 },
		"the query of the dropped flag is released, the one of the kept flag stays")

	_, ok := svc.segmentQueries.load(`targetingKey in segment("beta")`)
	assert.True(t, ok)

	runtime.KeepAlive(kept)
}

func TestCompiledSegmentQueriesSharedByTwoFlagsStayUntilBothAreDropped(t *testing.T) {
	t.Parallel()

	svc := NewService()
	query := `targetingKey in segment("beta")`

	first := segmentFlag(query)
	require.NoError(t, first.IsValid(svc))

	func() {
		require.NoError(t, segmentFlag(query).IsValid(svc)) // the second flag is dropped
	}()

	gcUntil(
		t,
		func() bool { return svc.segmentQueries.refCount(query) == 1 },
		"the dropped flag released its reference",
	)
	assert.Equal(t, 1, svc.segmentQueries.size(), "the first flag still uses the query")

	runtime.KeepAlive(first)
}

// The cleanup of a flag that was loaded before a Close must not release what a flag loaded after it retained.
func TestReleaseOfAnEarlierEpochIsIgnored(t *testing.T) {
	t.Parallel()

	svc := NewService()
	query := `targetingKey in segment("beta")`

	before := svc.segmentQueries.epoch.Load()

	svc.Close()
	assert.Equal(t, 0, svc.segmentQueries.size())

	again := segmentFlag(query)
	require.NoError(t, again.IsValid(svc))

	svc.segmentQueries.release(before, []string{query}) // what the cleanup of a flag of the first epoch does

	assert.Equal(t, 1, svc.segmentQueries.size())
	assert.Equal(t, 1, svc.segmentQueries.refCount(query))

	runtime.KeepAlive(again)
}

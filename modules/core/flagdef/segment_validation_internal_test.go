package flagdef

import (
	"context"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/rules/parser"
)

// rulesBetaQuery is a Rules query that targets the segment beta.
const rulesBetaQuery = `targetingKey in segment("beta")`

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
// kept with the rules, the Service memoizes nothing for them. Each query format is evaluated on its own, so the rule
// of each format is the one that decides.
func TestFirstEvaluationAfterLoadCompilesNothing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		query       string
		wantVariant string
	}{
		{name: "jsonlogic", query: `{"inSegment": [{"var": "targetingKey"}, "beta"]}`, wantVariant: "on"},
		{name: "rules", query: rulesBetaQuery, wantVariant: "on"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := NewService()
			flag := segmentFlag(tt.query)
			require.NoError(t, flag.IsValid(svc))

			assert.Equal(t, 1, svc.segmentQueries.size(), "the validation compiled the query")
			assert.Equal(t, 0, mapSize(&svc.compiled))

			evaluators := mapSize(&svc.nikunjy.entries)

			for range 2 {
				_, details := flag.Value(
					"f",
					ffcontext.NewEvaluationContext("k"),
					Context{Service: svc, Segments: allMembers{}},
				)
				assert.Equal(t, tt.wantVariant, details.Variant)
			}

			assert.Equal(t, 1, svc.segmentQueries.size())
			assert.Equal(t, 0, mapSize(&svc.compiled), "nothing was memoized by the evaluations")
			assert.Equal(t, evaluators, mapSize(&svc.nikunjy.entries), "no evaluator was parsed by the evaluations")

			runtime.KeepAlive(flag) // the flag keeps its queries registered until the checks are done
		})
	}
}

// A JsonLogic query that writes the operator with a JSON escape has no literal marker: it is retained at load and
// released with its flag all the same, and the fallback cache never sees it.
func TestEscapedInSegmentIsRetainedAndReleasedWithItsFlag(t *testing.T) {
	t.Parallel()

	svc := NewService()
	query := `{"\u0069nSegment": [{"var": "targetingKey"}, "beta"]}`

	func() {
		flag := segmentFlag(query)
		require.NoError(t, flag.IsValid(svc))

		assert.Equal(t, 1, svc.segmentQueries.size(), "the query is retained at load")
		assert.Equal(t, 1, svc.segmentQueries.refCount(query))

		_, details := flag.Value(
			"f",
			ffcontext.NewEvaluationContext("k"),
			Context{Service: svc, Segments: allMembers{}},
		)
		assert.Equal(t, "on", details.Variant)
		assert.Equal(t, 0, mapSize(&svc.compiled), "the fallback cache did not compile it")
	}()

	gcUntil(t, func() bool { return svc.segmentQueries.size() == 0 }, "the query is released with the flag")
	assert.Equal(t, 0, svc.segmentQueries.refCount(query))
	assert.Equal(t, 0, mapSize(&svc.compiled), "and nothing is left in the fallback cache")
}

// What a flag brought stays only as long as the flag: a reload that drops it drops the compiled queries.
func TestCompiledSegmentQueriesLiveWithTheirFlag(t *testing.T) {
	t.Parallel()

	svc := NewService()

	load := func(query string) {
		require.NoError(t, segmentFlag(query).IsValid(svc)) // the flag is not kept
	}

	load(`{"inSegment": [{"var": "targetingKey"}, "beta"]}`)

	kept := segmentFlag(rulesBetaQuery)
	require.NoError(t, kept.IsValid(svc))

	gcUntil(t, func() bool { return svc.segmentQueries.size() == 1 },
		"the query of the dropped flag is released, the one of the kept flag stays")

	_, ok := svc.segmentQueries.load(rulesBetaQuery)
	assert.True(t, ok)

	runtime.KeepAlive(kept)
}

func TestCompiledSegmentQueriesSharedByTwoFlagsStayUntilBothAreDropped(t *testing.T) {
	t.Parallel()

	svc := NewService()
	query := rulesBetaQuery

	first := segmentFlag(query)
	require.NoError(t, first.IsValid(svc))

	func() {
		second := segmentFlag(query)
		require.NoError(t, second.IsValid(svc)) // the second flag is dropped after this block
		assert.Equal(t, 2, svc.segmentQueries.refCount(query), "both flags reference the query")
		runtime.KeepAlive(second)
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
	query := rulesBetaQuery

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

// The query that a scheduled step brings is parsed by the validation and stays parsed as long as the flag: the first
// evaluation after the step is due parses nothing.
func TestScheduledStepQueryIsParsedWithTheFlag(t *testing.T) {
	t.Parallel()

	svc := NewService()
	query := `targetingKey in segment("late")`

	load := func() *InternalFlag {
		flag := segmentFlag(`age gt 1`)
		patch := InternalFlag{Rules: &[]Rule{{Name: new("r0"), Query: new(query)}}}
		step := ScheduledStep{Date: new(time.Unix(1, 0)), InternalFlag: patch}
		flag.Scheduled = &[]ScheduledStep{step}

		return flag
	}

	kept := load()
	require.NoError(t, kept.IsValid(svc))

	_, parsed := loadPtr[parser.Evaluator](&svc.nikunjy.entries, query)
	assert.True(t, parsed, "the query of the step is parsed at load")
	assert.Equal(t, 1, svc.segmentQueries.size(), "and its compiled form is kept")

	func() {
		require.NoError(t, load().IsValid(svc)) // a second flag with the same step, dropped
	}()

	gcUntil(t, func() bool {
		svc.nikunjy.refsMu.Lock()
		defer svc.nikunjy.refsMu.Unlock()

		return svc.nikunjy.refs[query] == 1
	}, "the dropped flag released its reference")

	_, parsed = loadPtr[parser.Evaluator](&svc.nikunjy.entries, query)
	assert.True(t, parsed, "the kept flag still holds it")

	_, details := kept.Value("f", ffcontext.NewEvaluationContext("k"), Context{Service: svc, Segments: allMembers{}})
	assert.Equal(t, "on", details.Variant, "the step is due: its rule decides and the key is a member")
	assert.Equal(t, segment.StatusHealthy, details.SegmentStatus)

	runtime.KeepAlive(kept)
}

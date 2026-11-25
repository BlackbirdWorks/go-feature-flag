package flag

import (
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"
	"unsafe"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
	"github.com/blackbirdworks/semaphore/rules/parser"
)

const (
	keyEqA      = `key eq "a"`
	keyEqB      = `key eq "b"`
	nobody      = "nobody"
	user0000001 = "user-0000001"
)

// newIndexingService creates a Service that gives keys to the short queries of the tests too.
func newIndexingService() *Service {
	svc := NewService()
	svc.nikunjy.keysMinQueryLen = 0

	return svc
}

// idList returns `key in ["user-0000000", ...]` with n ids, where the id at position
// replaced (if it is not negative) is changed to replacement.
func idList(count, replaced int, replacement string) string {
	var builder strings.Builder
	builder.Grow(count*16 + 16)
	builder.WriteString(`key in [`)

	for i := range count {
		if i > 0 {
			builder.WriteString(", ")
		}

		if i == replaced {
			fmt.Fprintf(&builder, `"%s"`, replacement)

			continue
		}

		fmt.Fprintf(&builder, `"user-%07d"`, i)
	}

	builder.WriteString(`]`)

	return builder.String()
}

// applies reports whether the rule applies to the evaluation context of key.
func applies(svc *Service, r *Rule, key string) bool {
	_, err := r.Evaluate(svc, key, ffcontext.NewEvaluationContext(key), "test-flag", false)

	return err == nil
}

func validatedFlag(tb testing.TB, svc *Service, queries ...string) *InternalFlag {
	tb.Helper()

	f := newFlagWithQueries(queries...)
	require.NoError(tb, f.IsValid(svc))

	return f
}

func indexedEvaluator(c *nikunjyEvaluatorCache, r *Rule) *parser.Evaluator {
	return c.lookup(r.QueryHash, r.GetQuery())
}

func indexSize(evalCache *nikunjyEvaluatorCache) int {
	count := 0

	evalCache.byKey.Range(func(_, _ any) bool {
		count++

		return true
	})

	return count
}

// assertSharedEvaluators checks that every rule of flags is found by its hash, that rules with
// one text share its bytes, and that wantEvaluators distinct evaluators are behind the rules.
func assertSharedEvaluators(
	t *testing.T, evalCache *nikunjyEvaluatorCache, flags []*InternalFlag, wantEvaluators int,
) {
	t.Helper()

	distinct := map[*parser.Evaluator]string{}
	bytesOf := map[string]*byte{}

	for _, flg := range flags {
		for i := range *flg.Rules {
			rule := &(*flg.Rules)[i]
			evaluator := indexedEvaluator(evalCache, rule)
			require.NotNil(t, evaluator, "a validated rule must be found by its hash")

			distinct[evaluator] = rule.GetQuery()
			if shared, ok := bytesOf[rule.GetQuery()]; ok {
				assert.Same(t, shared, unsafe.StringData(rule.GetQuery()), "rules with one text share its bytes")
			}

			bytesOf[rule.GetQuery()] = unsafe.StringData(rule.GetQuery())
			want, err := evalCache.get(rule.GetTrimmedQuery())
			require.NoError(t, err)
			assert.Same(t, want, evaluator, "the hash lookup must give the evaluator of the query text")
		}
	}

	assert.Len(t, distinct, wantEvaluators)
}

func TestNikunjyEvaluatorCache_Lookup(t *testing.T) {
	t.Parallel()

	const query = `key eq "shared"`

	tests := []struct {
		// queryOf returns the query of rule r of flag f.
		queryOf func(f, r int) string
		name    string
		flags   int
		rules   int
		// wantEvaluators is the number of distinct evaluators behind all the rules.
		wantEvaluators int
	}{
		{
			name:           "the same text in many rules of many flags shares one evaluator",
			flags:          20,
			rules:          5,
			queryOf:        func(_, _ int) string { return strings.Clone(query) },
			wantEvaluators: 1,
		},
		{
			name:           "different texts have different evaluators",
			flags:          4,
			rules:          3,
			queryOf:        func(f, r int) string { return fmt.Sprintf(`key eq "shared-%d-%d"`, f, r) },
			wantEvaluators: 12,
		},
		{
			name:  "texts that differ by whitespace or case are different queries",
			flags: 1,
			rules: 4,
			queryOf: func(_, r int) string {
				return []string{`key eq "shared"`, `key  eq "shared"`, `KEY eq "shared"`, `key eq "shared" `}[r]
			},
			wantEvaluators: 4,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := newIndexingService()
			evalCache := svc.nikunjy

			flags := make([]*InternalFlag, tt.flags)
			for flagIdx := range flags {
				queries := make([]string, tt.rules)
				for ruleIdx := range queries {
					queries[ruleIdx] = tt.queryOf(flagIdx, ruleIdx)
				}

				flags[flagIdx] = validatedFlag(t, svc, queries...)
			}

			assertSharedEvaluators(t, evalCache, flags, tt.wantEvaluators)
			runtime.KeepAlive(flags)
		})
	}
}

// splitRulesByQuery returns the rules of flags whose query is listA, and the other ones.
func splitRulesByQuery(flags []*InternalFlag, listA string) ([]*Rule, []*Rule) {
	var rulesA, rulesB []*Rule

	for _, f := range flags {
		for i := range *f.Rules {
			r := &(*f.Rules)[i]
			if r.GetQuery() == listA {
				rulesA = append(rulesA, r)
			} else {
				rulesB = append(rulesB, r)
			}
		}
	}

	return rulesA, rulesB
}

// assertListsHaveOwnEvaluators checks that the rules of a list share an evaluator, and that the
// two lists never share one.
func assertListsHaveOwnEvaluators(t *testing.T, evalCache *nikunjyEvaluatorCache, rulesA, rulesB []*Rule) {
	t.Helper()

	evA, evB := indexedEvaluator(evalCache, rulesA[0]), indexedEvaluator(evalCache, rulesB[0])
	require.NotNil(t, evA)
	require.NotNil(t, evB)
	assert.NotSame(t, evA, evB, "two different queries must never share an evaluator")

	for _, r := range rulesA {
		assert.Same(t, evA, indexedEvaluator(evalCache, r))
	}

	for _, r := range rulesB {
		assert.Same(t, evB, indexedEvaluator(evalCache, r))
	}
}

// assertListsApply checks which ids apply to the rules of list A and to the rules of list B.
func assertListsApply(t assert.TestingT, svc *Service, rulesA, rulesB []*Rule, idA, idB string) {
	for _, r := range rulesA {
		assert.True(t, applies(svc, r, idA), "the id of list A must match list A")
		assert.False(t, applies(svc, r, idB), "the id of list B must not match list A")
		assert.True(t, applies(svc, r, user0000001))
		assert.False(t, applies(svc, r, nobody))
	}

	for _, r := range rulesB {
		assert.True(t, applies(svc, r, idB), "the id of list B must match list B")
		assert.False(t, applies(svc, r, idA), "the id of list A must not match list B")
		assert.True(t, applies(svc, r, user0000001))
		assert.False(t, applies(svc, r, nobody))
	}
}

// runConcurrently runs check from 8 goroutines, 3 times each.
func runConcurrently(t *testing.T, check func(t assert.TestingT)) {
	t.Helper()

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 3 {
				check(t)
			}
		})
	}

	wg.Wait()
}

func TestNikunjyEvaluatorCache_NearIdenticalLargeQueries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		// listA and listB differ in exactly one id (or in one more id).
		listB     func(n int) string
		name      string
		wantOnlyA string // only in A
		wantOnlyB string // only in B
		n         int
	}{
		{
			name:      "300k ids, one id differs, same length",
			n:         300000,
			listB:     func(n int) string { return idList(n, n/2, "usex-0150000") },
			wantOnlyA: "user-0150000",
			wantOnlyB: "usex-0150000",
		},
		{
			name:      "20k ids, the last id differs, same length",
			n:         20000,
			listB:     func(n int) string { return idList(n, n-1, "usex-0019999") },
			wantOnlyA: "user-0019999",
			wantOnlyB: "usex-0019999",
		},
		{
			name:      "ids, one id is longer, different length",
			n:         20000,
			listB:     func(n int) string { return idList(n, n/2, "user-0010000-longer") },
			wantOnlyA: "user-0010000",
			wantOnlyB: "user-0010000-longer",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := newIndexingService()
			evalCache := svc.nikunjy
			listA, listB := idList(tt.n, -1, ""), tt.listB(tt.n)
			require.NotEqual(t, listA, listB)

			// Both lists are in several flags, in different rule positions, with their
			// own copy of the text.
			flags := []*InternalFlag{
				validatedFlag(t, svc, strings.Clone(listA), strings.Clone(listB)),
				validatedFlag(t, svc, strings.Clone(listB), strings.Clone(listA)),
				validatedFlag(t, svc, strings.Clone(listA)),
			}
			rulesA, rulesB := splitRulesByQuery(flags, listA)
			require.Len(t, rulesA, 3)
			require.Len(t, rulesB, 2)
			assertListsHaveOwnEvaluators(t, evalCache, rulesA, rulesB)

			check := func(t assert.TestingT) {
				assertListsApply(t, svc, rulesA, rulesB, tt.wantOnlyA, tt.wantOnlyB)
			}
			check(t)
			t.Run("interleaved and concurrent evaluations", func(t *testing.T) {
				t.Parallel()

				runConcurrently(t, check)
			})
			runtime.KeepAlive(flags)
		})
	}
}

func TestNikunjyEvaluatorCache_LookupIsExact(t *testing.T) {
	t.Parallel()

	tests := []struct {
		// mutate changes a validated rule, or returns another rule to evaluate.
		mutate func(r *Rule) *Rule
		name   string
		// matches and wantNoMatch are keys that must (not) apply to the rule afterwards.
		wantMatches, wantNoMatch string
		// wantIndexed is whether the rule can still be found by its hash.
		wantIndexed bool
	}{
		{
			name:        "unchanged rule",
			mutate:      func(r *Rule) *Rule { return r },
			wantIndexed: true,
			wantMatches: "a", wantNoMatch: "b",
		},
		{
			name: "copy of the rule keeps the hash",
			mutate: func(r *Rule) *Rule {
				c := *r

				return &c
			},
			wantIndexed: true,
			wantMatches: "a", wantNoMatch: "b",
		},
		{
			name: "query rewritten through its pointer is never evaluated with the old evaluator",
			mutate: func(r *Rule) *Rule {
				*r.Query = keyEqB

				return r
			},
			wantIndexed: false,
			wantMatches: "b", wantNoMatch: "a",
		},
		{
			name: "another query with the hash of the old one",
			mutate: func(r *Rule) *Rule {
				q := keyEqB
				c := *r
				c.Query = &q

				return &c
			},
			wantIndexed: false,
			wantMatches: "b", wantNoMatch: "a",
		},
		{
			name: "same text in another string keeps the evaluator",
			mutate: func(r *Rule) *Rule {
				c := *r
				q := strings.Clone(*r.Query)
				c.Query = &q

				return &c
			},
			wantIndexed: true,
			wantMatches: "a", wantNoMatch: "b",
		},
		{
			name: "merging a query resets the hash",
			mutate: func(r *Rule) *Rule {
				q := keyEqB
				r.MergeRules(Rule{Query: &q})

				return r
			},
			wantIndexed: false,
			wantMatches: "b", wantNoMatch: "a",
		},
		{
			name: "hash that is not the one of any query",
			mutate: func(r *Rule) *Rule {
				c := *r
				c.QueryHash = 12345

				return &c
			},
			wantIndexed: false,
			wantMatches: "a", wantNoMatch: "b",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := newIndexingService()
			c := svc.nikunjy
			flg := validatedFlag(t, svc, keyEqA)
			require.NotZero(t, (*flg.Rules)[0].QueryHash)
			r := tt.mutate(&(*flg.Rules)[0])
			assert.Equal(t, tt.wantIndexed, indexedEvaluator(c, r) != nil)
			assert.True(t, applies(svc, r, tt.wantMatches))
			assert.False(t, applies(svc, r, tt.wantNoMatch))
			runtime.KeepAlive(flg)
		})
	}
}

// assertOneTextPerKeyAndEvaluator checks that every rule of flags is found by its hash, that no
// key and no evaluator belongs to two texts, and that there are n of each.
func assertOneTextPerKeyAndEvaluator(
	t *testing.T,
	evalCache *nikunjyEvaluatorCache,
	flags []*InternalFlag,
	count int,
) {
	t.Helper()

	keys := map[uint64]string{}
	evaluators := map[*parser.Evaluator]string{}

	for _, f := range flags {
		for i := range *f.Rules {
			r := &(*f.Rules)[i]
			eval := indexedEvaluator(evalCache, r)
			require.NotNil(t, eval)

			if other, ok := keys[r.QueryHash]; ok {
				require.Equal(t, other, r.GetQuery(), "a key must belong to one text")
			}

			keys[r.QueryHash] = r.GetQuery()
			if other, ok := evaluators[eval]; ok {
				require.Equal(t, other, r.GetQuery(), "an evaluator must belong to one text")
			}

			evaluators[eval] = r.GetQuery()
		}
	}

	assert.Len(t, keys, count)
	assert.Len(t, evaluators, count)
	assert.NotContains(t, keys, uint64(0))
}

// assertCollidingQueriesApply checks that each rule of flags applies to the key of its own query
// only, among the count colliding queries.
func assertCollidingQueriesApply(t *testing.T, svc *Service, flags []*InternalFlag, count int) {
	t.Helper()

	for _, f := range flags {
		for i := range *f.Rules {
			r := &(*f.Rules)[i]

			for j := range count {
				want := fmt.Sprintf("collide-%d", j)
				assert.Equal(t, r.GetQuery() == fmt.Sprintf("key eq %q", want), applies(svc, r, want))
			}
		}
	}
}

// TestNikunjyEvaluatorCache_ForcedCollisions makes every query hash to the same value, which
// is the worst case of a 64-bit hash: no two queries may share an evaluator.
func TestNikunjyEvaluatorCache_ForcedCollisions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		hash func(string) uint64
		name string
		// wantQueries is the number of distinct queries that must each get their own text and evaluator
		wantQueries int
	}{
		{name: "every query has the same hash", wantQueries: 10, hash: func(string) uint64 { return 42 }},
		{
			name:        "every query hashes to zero, the key of rules without a hash",
			wantQueries: 10,
			hash:        func(string) uint64 { return 0 },
		},
		{name: "every query hashes to the last key", wantQueries: 10, hash: func(string) uint64 { return ^uint64(0) }},
		{name: "queries collide in pairs", wantQueries: 10, hash: func(q string) uint64 { return uint64(len(q) % 2) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := newIndexingService()
			evalCache := svc.nikunjy
			evalCache.hash = tt.hash

			queries := make([]string, 0, tt.wantQueries)
			for i := range tt.wantQueries {
				queries = append(queries, fmt.Sprintf(`key eq "collide-%d"`, i))
			}
			// A second flag with the same texts, in another order and in other strings.
			reversed := make([]string, len(queries))
			for i, q := range queries {
				reversed[len(queries)-1-i] = strings.Clone(q)
			}

			flags := []*InternalFlag{validatedFlag(t, svc, queries...), validatedFlag(t, svc, reversed...)}

			assertOneTextPerKeyAndEvaluator(t, evalCache, flags, len(queries))
			assertCollidingQueriesApply(t, svc, flags, len(queries))

			// Releasing one flag keeps the keys of the other.
			flags[0] = nil

			gcUntil(
				t,
				func() bool { return evalCache.keyRefsTotal() == len(queries) },
				"the collected flag must release its keys",
			)

			for i := range *flags[1].Rules {
				r := &(*flags[1].Rules)[i]
				assert.NotNil(t, indexedEvaluator(evalCache, r))
			}

			runtime.KeepAlive(flags[1])
		})
	}
}

func (c *nikunjyEvaluatorCache) keyRefsTotal() int {
	c.keysMu.Lock()
	defer c.keysMu.Unlock()

	n := 0
	for _, refs := range c.keyRefs {
		n += refs
	}

	return n
}

func TestRule_QueryHashIsNotSerialized(t *testing.T) {
	t.Parallel()

	svc := newIndexingService()
	f := validatedFlag(t, svc, keyEqA)
	require.NotZero(t, (*f.Rules)[0].QueryHash)

	asJSON, err := json.Marshal(f)
	require.NoError(t, err)
	assert.NotContains(t, strings.ToLower(string(asJSON)), "hash")

	var decoded InternalFlag
	require.NoError(
		t,
		json.Unmarshal([]byte(`{"targeting":[{"query":"key eq \"a\"","variation":"on","queryHash":7}]}`), &decoded),
	)
	assert.Zero(t, (*decoded.Rules)[0].QueryHash)
}

func TestRule_QueryHashDoesNotMakeFlagsDiffer(t *testing.T) {
	t.Parallel()

	svc := newIndexingService()
	loaded := validatedFlag(t, svc, keyEqA)
	reloaded := validatedFlag(t, svc, strings.Clone(keyEqA))
	assert.True(t, cmp.Equal(loaded, reloaded), "two loads of the same flag are the same flag")
	assert.False(t, cmp.Equal(loaded, validatedFlag(t, svc, keyEqB)))
}

func TestNikunjyEvaluatorCache_NotIndexed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		query       string
		wantMatches string
		wantNoMatch string
		// valid is whether the flag is valid.
		wantValid bool
	}{
		{
			name:        "jsonlogic query",
			query:       `{"==": [{"var": "key"}, "a"]}`,
			wantValid:   true,
			wantMatches: "a",
			wantNoMatch: "b",
		},
		{
			name:        "multi-line nikunjy query is indexed by its text before trimming",
			query:       "key eq \n    \"a\"",
			wantValid:   true,
			wantMatches: "a",
			wantNoMatch: "b",
		},
		{
			name:      "invalid query",
			query:     `key eq`,
			wantValid: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := newIndexingService()
			evalCache := svc.nikunjy
			flg := newFlagWithQueries(tt.query)

			err := flg.IsValid(svc)
			if !tt.wantValid {
				require.Error(t, err)
				assert.Zero(t, indexSize(evalCache), "an invalid flag must not index its queries")

				return
			}

			require.NoError(t, err)

			rule := &(*flg.Rules)[0]
			assert.Equal(t, strings.Contains(tt.query, "eq"), indexedEvaluator(evalCache, rule) != nil)
			assert.True(t, applies(svc, rule, tt.wantMatches))
			assert.False(t, applies(svc, rule, tt.wantNoMatch))
			runtime.KeepAlive(flg)
		})
	}
}

func TestNikunjyEvaluatorCache_UnvalidatedRuleEvaluates(t *testing.T) {
	t.Parallel()

	svc := newIndexingService()
	q := keyEqA
	r := &Rule{Query: &q, VariationResult: &q}
	assert.True(t, applies(svc, r, "a"))
	assert.False(t, applies(svc, r, "b"))
}

func TestNikunjyEvaluatorCache_Reload(t *testing.T) {
	t.Parallel()

	svc := newIndexingService()
	evalCache := svc.nikunjy

	const flags = 10

	load := func(query func(f int) string) []*InternalFlag {
		set := make([]*InternalFlag, flags)
		for f := range set {
			set[f] = validatedFlag(t, svc, query(f))
		}

		return set
	}
	current := load(func(f int) string { return fmt.Sprintf(`key eq "gen-0-%d"`, f%3) })
	check := func(set []*InternalFlag, gen int) {
		for idx, flg := range set {
			r := &(*flg.Rules)[0]
			assert.NotNil(t, indexedEvaluator(evalCache, r))
			assert.True(t, applies(svc, r, fmt.Sprintf("gen-%d-%d", gen, idx%3)))
			assert.False(
				t,
				applies(svc, r, fmt.Sprintf("gen-%d-%d", gen-1, idx%3)),
				"stale evaluator of the previous load",
			)
		}
	}
	check(current, 0)

	for gen := 1; gen <= 30; gen++ {
		// Even generations reload the same queries, the others change them.
		changed := gen%2 == 1

		next := load(func(f int) string {
			if changed {
				return fmt.Sprintf(`key eq "gen-%d-%d"`, gen, f%3)
			}

			return fmt.Sprintf(`key eq "gen-%d-%d"`, gen-1, f%3)
		})
		if changed {
			check(next, gen)
		} else {
			check(next, gen-1)
		}

		runtime.KeepAlive(current)
		current = next

		runtime.GC()
	}

	gcUntil(t, func() bool { return indexSize(evalCache) <= flags }, "the rules of the old loads must be unindexed")
	assert.LessOrEqual(t, indexSize(evalCache), flags)
	runtime.KeepAlive(current)

	clear(current) // drop the last references to the flags
	gcUntil(t, func() bool { return indexSize(evalCache) == 0 }, "no rule left to index")

	cached := 0

	evalCache.entries.Range(func(_, _ any) bool {
		cached++

		return true
	})
	assert.Zero(t, cached)
	runtime.KeepAlive(current)
}

func TestNikunjyEvaluatorCache_ConcurrentEvaluationsDuringReloads(t *testing.T) {
	t.Parallel()

	svc := newIndexingService()
	listA, listB := idList(2000, -1, ""), idList(2000, 1000, "usex-0001000")

	var current struct {
		flags []*InternalFlag
		mu    sync.RWMutex
	}

	newSet := func(i int) []*InternalFlag {
		a, b := listA, listB
		if i%2 == 1 {
			a, b = listB, listA
		}

		return []*InternalFlag{validatedFlag(t, svc, strings.Clone(a)), validatedFlag(t, svc, strings.Clone(b))}
	}
	current.flags = newSet(0)

	stop := make(chan struct{})

	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}

				current.mu.RLock()
				set := current.flags
				current.mu.RUnlock()
				// Whatever the set, one flag has list A and the other list B.
				for _, f := range set {
					r := &(*f.Rules)[0]
					isA := strings.Contains(r.GetQuery(), "user-0001000")
					assert.Equal(t, isA, applies(svc, r, "user-0001000"))
					assert.Equal(t, !isA, applies(svc, r, "usex-0001000"))
				}
			}
		})
	}

	for i := 1; i <= 20; i++ {
		next := newSet(i)

		current.mu.Lock()
		current.flags = next
		current.mu.Unlock()
		runtime.GC()
	}

	close(stop)
	wg.Wait()
}

func TestHashNikunjyQuery(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("0123456789abcdef", 8)

	tests := []struct {
		name string
		a, b string
		// wantEqual is whether the two queries must have the same hash.
		wantEqual bool
	}{
		{name: "same text in two strings", a: strings.Clone(long), b: strings.Clone(long), wantEqual: true},
		{name: "empty and a zero byte", a: "", b: "\x00"},
		{name: "a text and the text with a zero byte", a: abc, b: "abc\x00"},
		{name: "one byte of a short text", a: keyEqA, b: keyEqB},
		{name: "one byte of a long text, first block", a: long, b: "1" + long[1:]},
		{name: "one byte of a long text, middle", a: long, b: long[:50] + "X" + long[51:]},
		{name: "one byte of a long text, tail", a: long, b: long[:len(long)-1] + "X"},
		{
			name: "swapped blocks of 8 bytes",
			a:    long[:8] + "ABCDEFGH" + long[16:],
			b:    "ABCDEFGH" + long[8:16] + long[16:],
		},
		{name: "case", a: keyEqA, b: `KEY eq "a"`},
		{name: "whitespace", a: keyEqA, b: `key  eq "a"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.wantEqual, hashNikunjyQuery(tt.a) == hashNikunjyQuery(tt.b))
		})
	}

	t.Run("every length from 0 to 100 has a different hash and is stable", func(t *testing.T) {
		t.Parallel()

		seen := map[uint64]int{}

		for n := range 101 {
			h := hashNikunjyQuery(strings.Repeat("a", n))
			assert.Equal(t, h, hashNikunjyQuery(strings.Repeat("a", n)))
			other, collided := seen[h]
			assert.False(t, collided, "lengths %d and %d have the same hash", other, n)
			seen[h] = n
		}
	})
}

func TestNikunjyEvaluatorCache_ValidatingAgain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		validations int
		// wantRefs is the number of key references the validations take: one per rule and validation
		wantRefs int
	}{
		{name: "validated once", validations: 1, wantRefs: 2},
		{name: "validated again, as the linter or a copy of a cache does", validations: 3, wantRefs: 6},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := newIndexingService()
			evalCache := svc.nikunjy

			flg := newFlagWithQueries(keyEqA, keyEqB)
			for range tt.validations {
				require.NoError(t, flg.IsValid(svc))
			}

			hashes := []uint64{(*flg.Rules)[0].QueryHash, (*flg.Rules)[1].QueryHash}
			assert.NotZero(t, hashes[0])
			assert.NotEqual(t, hashes[0], hashes[1])
			assert.Equal(t, tt.wantRefs, evalCache.keyRefsTotal(), "one reference per rule and validation")
			assert.Equal(t, 2, indexSize(evalCache))
			assert.True(t, applies(svc, &(*flg.Rules)[0], "a"))
			assert.False(t, applies(svc, &(*flg.Rules)[1], "a"))

			runtime.KeepAlive(flg)
			*flg = InternalFlag{} // drop the rules

			gcUntil(
				t,
				func() bool { return indexSize(evalCache) == 0 },
				"the keys of a collected flag must be released",
			)
		})
	}
}

func TestRule_validateReturnsTheEvaluator(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		rule        Rule
		isDefault   bool
		wantEval    bool
		wantInvalid bool
	}{
		{name: "nikunjy query", rule: Rule{Query: new(keyEqA), VariationResult: new("on")}, wantEval: true},
		{name: "jsonlogic query", rule: Rule{Query: new(`{"==": [1, 1]}`), VariationResult: new("on")}},
		{name: "default rule", rule: Rule{VariationResult: new("on")}, isDefault: true},
		{
			name: "disabled rule",
			rule: Rule{Query: new(`key eq `), Disable: new(true), VariationResult: new("on")},
		},
		{name: "invalid query", rule: Rule{Query: new(`key eq `), VariationResult: new("on")}, wantInvalid: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := NewService()

			eval, err := tt.rule.validate(svc, tt.isDefault, map[string]*any{"on": new(any(true))}, nil)
			if tt.wantInvalid {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			assert.Equal(t, tt.wantEval, eval != nil)
		})
	}
}

func TestNikunjyEvaluatorCache_Disabled(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		enabled     bool
		wantIndexed bool
	}{
		{name: "keys are saved when the cleanups that release them run", enabled: true, wantIndexed: true},
		{name: "keys are not saved when the cleanups never run", enabled: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := newIndexingService()
			evalCache := svc.nikunjy
			evalCache.keysEnabled = tt.enabled

			flg := validatedFlag(t, svc, keyEqA)
			rule := &(*flg.Rules)[0]
			assert.Equal(t, tt.wantIndexed, indexedEvaluator(evalCache, rule) != nil)
			assert.Equal(t, tt.wantIndexed, indexSize(evalCache) == 1)
			assert.True(t, applies(svc, rule, "a"), "the rule evaluates through the evaluator cache")
			assert.False(t, applies(svc, rule, "b"))
			runtime.KeepAlive(flg)
		})
	}
}

func TestNikunjyEvaluatorCache_ShortQueriesHaveNoKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		query       string
		wantIndexed bool
	}{
		{name: "short query", query: keyEqA},
		{
			name:  "query just under the length",
			query: `key eq "` + strings.Repeat("a", defaultNikunjyKeysMinQueryLen-len(`key eq ""`)-1) + `"`,
		},
		{
			name:        "query of the length",
			query:       `key eq "` + strings.Repeat("a", defaultNikunjyKeysMinQueryLen-len(`key eq ""`)) + `"`,
			wantIndexed: true,
		},
		{name: "long query", query: idList(200, -1, ""), wantIndexed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := NewService()
			evalCache := svc.nikunjy
			flg := newFlagWithQueries(tt.query)
			require.NoError(t, flg.IsValid(svc))
			rule := &(*flg.Rules)[0]
			assert.Equal(t, tt.wantIndexed, indexedEvaluator(evalCache, rule) != nil)
			assert.Equal(t, tt.wantIndexed, rule.QueryHash != 0)
			assert.True(
				t,
				applies(svc, rule, user0000001) || !strings.Contains(tt.query, user3),
				"the rule evaluates either way",
			)
			runtime.KeepAlive(flg)
		})
	}
}

func TestNikunjyEvaluatorCache_KnownQueryIsNotLookedUpByText(t *testing.T) {
	t.Parallel()

	query := idList(200, -1, "")

	tests := []struct {
		name string
		// loads is the number of flags that carry the query, loaded one after the other.
		loads int
		// wantTextRefs is the references of the evaluator cache to the query after the loads: only the
		// first load looks the query up by its text, the others find its key.
		wantTextRefs int
		wantKeyRefs  int
	}{
		{name: "first load: the query is looked up by its text", loads: 1, wantTextRefs: 1, wantKeyRefs: 1},
		{name: "second load: found by its key", loads: 2, wantTextRefs: 1, wantKeyRefs: 2},
		{name: "many loads: found by their key", loads: 20, wantTextRefs: 1, wantKeyRefs: 20},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := newIndexingService()
			evalCache := svc.nikunjy

			flags := make([]*InternalFlag, tt.loads)
			for i := range flags {
				flags[i] = validatedFlag(t, svc, strings.Clone(query))
			}

			trimmed := (&(*flags[0].Rules)[0]).GetTrimmedQuery()
			assert.Equal(t, tt.wantTextRefs, refCount(evalCache, trimmed))
			assert.Equal(t, tt.wantKeyRefs, evalCache.keyRefsTotal())

			first := indexedEvaluator(evalCache, &(*flags[0].Rules)[0])
			require.NotNil(t, first)

			for _, flg := range flags {
				rule := &(*flg.Rules)[0]
				assert.Same(t, first, indexedEvaluator(evalCache, rule), "one evaluator for the query")
				assert.True(t, applies(svc, rule, "user-0000199"))
				assert.False(t, applies(svc, rule, nobody))
			}

			runtime.KeepAlive(flags)
			clear(flags)
			gcUntil(t, func() bool { return evalCache.keyRefsTotal() == 0 && refCount(evalCache, trimmed) == 0 },
				"every reference must be released once the flags are collected")
			assert.Zero(t, indexSize(evalCache))
		})
	}
}

func TestNikunjyEvaluatorCache_KnownQueryOfAnInvalidFlag(t *testing.T) {
	t.Parallel()

	query := idList(200, -1, "")

	tests := []struct {
		// invalid makes the flag that carries the known query fail its validation.
		invalid func(f *InternalFlag)
		name    string
		// wantRefsWhileInvalid is the number of key references while the invalid flag lives, wantRefsAfter once
		// it is collected
		wantRefsWhileInvalid, wantRefsAfter int
	}{
		{
			name:                 "a rule of the flag without variation",
			invalid:              func(f *InternalFlag) { *f.Rules = append(*f.Rules, Rule{Query: new(`key eq "x"`)}) },
			wantRefsWhileInvalid: 2, wantRefsAfter: 1,
		},
		{
			name:                 "a rule of the flag with a query that does not parse",
			invalid:              func(f *InternalFlag) { *f.Rules = append(*f.Rules, *newFlagWithQueries(`key eq`).Rules...) },
			wantRefsWhileInvalid: 2,
			wantRefsAfter:        1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := newIndexingService()
			evalCache := svc.nikunjy
			valid := validatedFlag(t, svc, strings.Clone(query))
			require.Equal(t, 1, evalCache.keyRefsTotal())

			invalid := newFlagWithQueries(strings.Clone(query))
			tt.invalid(invalid)
			require.Error(t, invalid.IsValid(svc))
			assert.Equal(
				t,
				tt.wantRefsWhileInvalid,
				evalCache.keyRefsTotal(),
				"the reference taken by the invalid flag is released with it",
			)

			runtime.KeepAlive(invalid)
			*invalid = InternalFlag{} // drop the rules

			gcUntil(
				t,
				func() bool { return evalCache.keyRefsTotal() == tt.wantRefsAfter },
				"the invalid flag must release its reference once",
			)
			assert.True(t, applies(svc, &(*valid.Rules)[0], "user-0000199"), "the query is still evaluated")
			runtime.KeepAlive(valid)
		})
	}
}

package flagdef_test

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

// minAllocs is the number of allocations of f in its cheapest run among allocRuns single runs. The pool of the query
// maps is dropped at random under the race detector, so an average is not stable: the cheapest run is the cost of an
// evaluation that finds its pooled map, whatever the detector does.
func minAllocs(f func()) float64 {
	cheapest := math.MaxFloat64

	for range allocRuns {
		cheapest = min(cheapest, testing.AllocsPerRun(1, f))
	}

	return cheapest
}

const allocRuns = 60

// TestEvaluationAllocations pins the allocations of one evaluation of a flag with one targeting rule. The numbers of
// the flags without segments were measured on the engine before the segments existed: they must not grow. A rule
// with a segment term allocates what the same rule without one allocates, plus the vector of the lookups that the
// provider fills (one allocation for the rule, whatever the number of terms).
//
//nolint:paralleltest // AllocsPerRun counts allocations from concurrent tests across the process
func TestEvaluationAllocations(t *testing.T) {
	provider := segment.NewMemoryProvider(map[string][]string{"beta": {keyAlice}, segChurned: {"bob"}})

	allocsOf := func(t *testing.T, query, key string) (float64, string) {
		t.Helper()

		svc := flagdef.NewService()
		flag := onOffFlag(queryRule("r", query))
		require.NoError(t, flag.IsValid(svc), query)

		evalCtx := ffcontext.NewEvaluationContextBuilder(key).AddCustom("plan", "free").Build()
		flagCtx := flagdef.Context{Service: svc, Segments: provider}

		_, details := flag.Value("f", evalCtx, flagCtx)

		return minAllocs(func() { flag.Value("f", evalCtx, flagCtx) }), details.Variant
	}

	t.Run("flags without segments allocate what they allocated before the segments", func(t *testing.T) {
		nikunjy, variant := allocsOf(t, `plan eq "pro"`, keyAlice)
		assert.Equal(t, variantOff, variant)
		assert.InDelta(t, 2, nikunjy, 0)

		jsonlogic, variant := allocsOf(t, `{"==": [{"var": "plan"}, "pro"]}`, keyAlice)
		assert.Equal(t, variantOff, variant)
		assert.InDelta(t, 10, jsonlogic, 0)
	})

	t.Run(
		"a typed attribute is converted once: its extra cost does not grow with the terms that read it",
		func(t *testing.T) {
			allocsWith := func(query string, user any) float64 {
				svc := flagdef.NewService()
				flag := onOffFlag(queryRule("r", query))
				require.NoError(t, flag.IsValid(svc))

				evalCtx := ffcontext.NewEvaluationContextBuilder("k").AddCustom("user", user).Build()
				flagCtx := flagdef.Context{Service: svc, Segments: provider}

				_, details := flag.Value("f", evalCtx, flagCtx)
				assert.Equal(t, variantOn, details.Variant, "alice is a member of both segments")

				return minAllocs(func() { flag.Value("f", evalCtx, flagCtx) })
			}

			plainUser := map[string]any{"id": keyAlice, "email": keyAlice}
			typedUser := userRecord{ID: keyAlice, Email: keyAlice}

			oneTerm, twoTerms := `user.id in segment("beta")`, `user.id in segment("beta") and user.email in segment("beta")`
			extraOne := allocsWith(oneTerm, typedUser) - allocsWith(oneTerm, plainUser)
			extraTwo := allocsWith(twoTerms, typedUser) - allocsWith(twoTerms, plainUser)

			assert.Positive(t, extraOne, "a typed attribute does cost its conversion")
			assert.InDelta(
				t,
				extraOne,
				extraTwo,
				0,
				"and a second term on the same attribute does not convert it again",
			)
		},
	)

	t.Run("a typed object that omits its only targeted identifier still pays its conversion", func(t *testing.T) {
		allocsWith := func(user any) (float64, string) {
			svc := flagdef.NewService()
			flag := onOffFlag(queryRule("r", `user.id in segment("beta")`))
			require.NoError(t, flag.IsValid(svc))

			evalCtx := ffcontext.NewEvaluationContextBuilder("k").AddCustom("user", user).Build()
			flagCtx := flagdef.Context{Service: svc, Segments: provider}

			_, details := flag.Value("f", evalCtx, flagCtx)

			return minAllocs(func() { flag.Value("f", evalCtx, flagCtx) }), details.Variant
		}

		typed, typedVariant := allocsWith(omittingUser{})
		plain, plainVariant := allocsWith(map[string]any{})

		assert.Equal(t, variantOff, typedVariant, "no identifier: not a member")
		assert.Equal(t, variantOff, plainVariant)
		assert.Greater(t, typed, plain, "the conversion of the typed object is paid even though nothing is looked up")
	})

	// each case compares the rule with a segment to a rule without one that selects the same variation
	tests := []struct {
		name        string
		query       string
		plainQuery  string
		key         string
		wantVariant string
		wantExtra   float64
	}{
		{
			name: "alloc: nikunjy member", query: nikunjyInclude, plainQuery: queryEqAlice,
			key: keyAlice, wantVariant: variantOn, wantExtra: 1,
		},
		{
			name: "alloc: nikunjy not member", query: nikunjyInclude, plainQuery: queryEqAlice,
			key: "zed", wantVariant: variantOff, wantExtra: 1,
		},
		{
			name:        "nikunjy include and exclude: two terms, one vector",
			query:       `targetingKey in segment("beta") and not (targetingKey in segment("churned"))`,
			plainQuery:  `targetingKey eq "alice" and not (plan eq "pro")`,
			key:         keyAlice,
			wantVariant: variantOn,
			wantExtra:   1,
		},
		{
			name:        "jsonlogic member",
			query:       jsonInclude,
			plainQuery:  queryEqAlice,
			key:         keyAlice,
			wantVariant: variantOn,
			wantExtra:   1,
		},
		{
			name:        "decided by a plain term: nothing is looked up",
			query:       `plan eq "pro" and targetingKey in segment("beta")`,
			plainQuery:  `plan eq "pro" and targetingKey eq "alice"`,
			key:         keyAlice,
			wantVariant: variantOff,
			wantExtra:   0,
		},
		{
			name: "decided by a plain term, or", query: `plan eq "free" or targetingKey in segment("beta")`,
			plainQuery: `plan eq "free" or targetingKey eq "zed"`, key: keyAlice, wantVariant: variantOn, wantExtra: 0,
		},
		{
			name:        "missing key: nothing is looked up",
			query:       nikunjyInclude,
			plainQuery:  queryEqAlice,
			key:         "",
			wantVariant: variantOff,
			wantExtra:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plain, plainVariant := allocsOf(t, tt.plainQuery, tt.key)
			withSegment, variant := allocsOf(t, tt.query, tt.key)

			assert.Equal(t, tt.wantVariant, variant)
			assert.Equal(t, tt.wantVariant, plainVariant, "the baseline selects the same variation")
			assert.LessOrEqual(
				t,
				withSegment,
				plain+tt.wantExtra,
				"the vector of the lookups is the only allocation of a segment rule",
			)
		})
	}
}

// userRecord is a typed attribute of a context.
type userRecord struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

// omittingUser is a typed attribute whose only identifier is left out of its conversion when it is empty.
type omittingUser struct {
	ID string `json:"id,omitempty"`
}

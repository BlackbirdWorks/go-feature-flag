package flagdef_test

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/testhelpers/clocktest"
)

const shapeDisabled = "rule disabled"

// shapeFlag builds a flag with one targeting rule (query) of the given rollout shape.
func shapeFlag(shape, query string) *flagdef.InternalFlag {
	now := clocktest.Instant()
	rule := flagdef.Rule{Name: new("r"), Query: new(query), VariationResult: new(variantOn)}
	flag := onOffFlag()
	flag.Metadata = &map[string]any{"segmentOwner": "team", "tags": []any{"a", "b"}}

	switch shape {
	case "variation":
	case "percentage":
		rule.VariationResult = nil
		rule.Percentages = &map[string]float64{variantOn: 60, variantOff: 40}
	case "progressive":
		rule.VariationResult = nil
		rule.ProgressiveRollout = &flagdef.ProgressiveRollout{
			Initial: &flagdef.ProgressiveRolloutStep{
				Variation: new(variantOff), Percentage: new(0.0), Date: new(now.Add(-time.Hour)),
			},
			End: &flagdef.ProgressiveRolloutStep{
				Variation: new(variantOn), Percentage: new(100.0), Date: new(now.Add(time.Hour)),
			},
		}
	case shapeDisabled:
		rule.Disable = new(true)
	case "scheduled":
		patch := flagdef.InternalFlag{Rules: &[]flagdef.Rule{{Name: new("r"), VariationResult: new(variantOff)}}}
		step := flagdef.ScheduledStep{Date: new(now.Add(-time.Minute)), InternalFlag: patch}
		flag.Scheduled = &[]flagdef.ScheduledStep{step}
	case "scheduled, not due":
		patch := flagdef.InternalFlag{Rules: &[]flagdef.Rule{{Name: new("r"), VariationResult: new(variantOff)}}}
		step := flagdef.ScheduledStep{Date: new(now.Add(time.Hour)), InternalFlag: patch}
		flag.Scheduled = &[]flagdef.ScheduledStep{step}
	case "experimentation":
		flag.Experimentation = &flagdef.ExperimentationRollout{
			Start: new(now.Add(-time.Hour)), End: new(now.Add(time.Hour)),
		}
	case "experimentation over":
		flag.Experimentation = &flagdef.ExperimentationRollout{
			Start: new(now.Add(-2 * time.Hour)), End: new(now.Add(-time.Hour)),
		}
	}

	rules := []flagdef.Rule{rule}
	flag.Rules = &rules

	return flag
}

func TestSegmentRuleIsEquivalentToTheListOfItsMembers(t *testing.T) {
	t.Parallel()

	rng := rand.New(rand.NewPCG(10, 14))

	members := make([]string, 0, 40)
	for range 40 {
		members = append(members, fmt.Sprintf("user-%04d", rng.IntN(2000)))
	}

	keys := append([]string{}, members...)
	for range 60 {
		keys = append(keys, fmt.Sprintf("user-%04d", rng.IntN(4000)))
	}

	quoted, err := json.Marshal(members)
	require.NoError(t, err)

	queries := map[string][2]string{
		"rules": {`targetingKey in segment("s")`, `targetingKey in ` + string(quoted)},
		"jsonlogic": {
			`{"inSegment": [{"var": "targetingKey"}, "s"]}`,
			`{"in": [{"var": "targetingKey"}, ` + string(quoted) + `]}`,
		},
	}
	shapes := []string{
		"variation", "percentage", "progressive", shapeDisabled, "scheduled", "scheduled, not due", "experimentation",
		"experimentation over",
	}
	provider := segment.NewMemoryProvider(map[string][]string{"s": members})

	for format, pair := range queries {
		for _, shape := range shapes {
			t.Run(format+"/"+shape, func(t *testing.T) {
				t.Parallel()

				svc := flagdef.NewService()
				viaSegment, viaList := shapeFlag(shape, pair[0]), shapeFlag(shape, pair[1])
				require.NoError(t, viaSegment.IsValid(svc))
				require.NoError(t, viaList.IsValid(svc))

				for _, key := range keys {
					evalCtx := ffcontext.NewEvaluationContext(key)

					gotValue, got := viaSegment.Value(
						"flag",
						evalCtx,
						flagdef.Context{Service: svc, Segments: provider},
					)
					wantValue, want := viaList.Value("flag", evalCtx, flagdef.Context{Service: svc})

					assert.Equal(t, wantValue, gotValue, "key %s", key)
					assert.Equal(t, want.Variant, got.Variant, "key %s", key)
					assert.Equal(t, want.Reason, got.Reason, "key %s", key)
					assert.Equal(t, want.RuleIndex, got.RuleIndex, "key %s", key)
					assert.Equal(t, want.RuleName, got.RuleName, "key %s", key)
					assert.Equal(t, want.ErrorCode, got.ErrorCode, "key %s", key)
					assert.Equal(t, want.ErrorMessage, got.ErrorMessage, "key %s", key)
					assert.Equal(t, want.Metadata, got.Metadata, "key %s", key)
				}
			})
		}
	}
}

// expr is a generated rule: an operator over operands, or a leaf.
type expr struct {
	left, right *expr
	kind        string // term, plain, fails, not, and, or
	segment     int
	pass        bool
}

const (
	exprTerm  = "term"
	exprPlain = "plain"
	exprFails = "fails"
	exprNot   = "not"
	exprAnd   = "and"
	exprOr    = "or"
)

// jsonList is a list of strings as a JSON array, which the Rules format reads as a list too.
func jsonList(ids []string) string {
	quoted := make([]string, 0, len(ids))
	for _, id := range ids {
		quoted = append(quoted, strconv.Quote(id))
	}

	return "[" + strings.Join(quoted, ", ") + "]"
}

func genExpr(rng *rand.Rand, depth int) *expr {
	if depth == 0 || rng.IntN(10) < 3 {
		switch rng.IntN(4) {
		case 0, 1:
			return &expr{kind: exprTerm, segment: rng.IntN(3)}
		case 2:
			return &expr{kind: exprPlain, pass: rng.IntN(2) == 0}
		default:
			return &expr{kind: exprFails}
		}
	}

	switch rng.IntN(5) {
	case 0:
		return &expr{kind: exprNot, left: genExpr(rng, depth-1)}
	case 1, 2:
		return &expr{kind: exprAnd, left: genExpr(rng, depth-1), right: genExpr(rng, depth-1)}
	default:
		return &expr{kind: exprOr, left: genExpr(rng, depth-1), right: genExpr(rng, depth-1)}
	}
}

// rules renders the rule in the Rules format. With members, a segment term is the list of the members.
func (e *expr) rules(members [][]string) string {
	switch e.kind {
	case exprTerm:
		if members == nil {
			return fmt.Sprintf(`targetingKey in segment("s%d")`, e.segment)
		}

		return `targetingKey in ` + jsonList(members[e.segment])
	case exprPlain:
		if e.pass {
			return `plan eq "pro"`
		}

		return `plan eq "free"`
	case exprFails:
		return `user.id eq "staff"` // fails when user is not an object
	case exprNot:
		return "not (" + e.left.rules(members) + ")"
	case exprAnd:
		return "(" + e.left.rules(members) + " and " + e.right.rules(members) + ")"
	default:
		return "(" + e.left.rules(members) + " or " + e.right.rules(members) + ")"
	}
}

// jsonLogic renders the rule in the JsonLogic format.
func (e *expr) jsonLogic(members [][]string) string {
	switch e.kind {
	case exprTerm:
		if members == nil {
			return fmt.Sprintf(`{"inSegment": [{"var": "targetingKey"}, "s%d"]}`, e.segment)
		}

		return `{"in": [{"var": "targetingKey"}, ` + jsonList(members[e.segment]) + `]}`
	case exprPlain:
		if e.pass {
			return `{"==": [{"var": "plan"}, "pro"]}`
		}

		return `{"==": [{"var": "plan"}, "free"]}`
	case exprFails:
		return `{"map": [{"var": "targetingKey"}, {"var": ""}]}` // fails: the key is not a list
	case exprNot:
		return `{"!": ` + e.left.jsonLogic(members) + `}`
	case exprAnd:
		return `{"and": [` + e.left.jsonLogic(members) + `, ` + e.right.jsonLogic(members) + `]}`
	default:
		return `{"or": [` + e.left.jsonLogic(members) + `, ` + e.right.jsonLogic(members) + `]}`
	}
}

// TestNestedSegmentRulesAreEquivalentToTheirListForm compares generated nested rules (depth up to 4, and, or, not,
// plain operands and operands that fail for some contexts) with the same rules where each segment term is the list of
// its members: whatever the list form evaluates, and whatever error it meets, the segment form does the same.
func TestNestedSegmentRulesAreEquivalentToTheirListForm(t *testing.T) {
	t.Parallel()

	rng := rand.New(rand.NewPCG(88, 14))

	pool := make([]string, 0, 24)
	for i := range 24 {
		pool = append(pool, fmt.Sprintf("user-%02d", i))
	}

	members := make([][]string, 3)
	byName := map[string][]string{}

	for i := range members {
		for _, id := range pool {
			if rng.IntN(3) == 0 {
				members[i] = append(members[i], id)
			}
		}

		byName[fmt.Sprintf("s%d", i)] = members[i]
	}

	provider := segment.NewMemoryProvider(byName)
	keys := slices.Concat(pool, []string{"stranger-1", "stranger-2"})
	users := []any{"a string", map[string]any{"id": "staff"}, map[string]any{"id": segOther}}

	rules := make([]*expr, 0, 120)
	for range 120 {
		rules = append(rules, genExpr(rng, 4))
	}

	for format, render := range map[string]func(*expr, [][]string) string{
		formatNikunjy: (*expr).rules,
		formatJSON:    (*expr).jsonLogic,
	} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()

			svc := flagdef.NewService()

			for _, rule := range rules {
				compareRuleForms(t, svc, provider, rule, render, members, keys, users)
			}
		})
	}
}

// compareRuleForms evaluates a rule in its segment form and in its list form for many contexts, and reports the
// first context where they differ.
func compareRuleForms(
	t *testing.T,
	svc *flagdef.Service,
	provider segment.Provider,
	rule *expr,
	render func(*expr, [][]string) string,
	members [][]string,
	keys []string,
	users []any,
) {
	t.Helper()

	viaSegment := onOffFlag(queryRule("r", render(rule, nil)))
	viaList := onOffFlag(queryRule("r", render(rule, members)))
	require.NoError(t, viaSegment.IsValid(svc), render(rule, nil))
	require.NoError(t, viaList.IsValid(svc), render(rule, members))

	for _, key := range keys {
		for _, plan := range []string{planPro, planFree} {
			for _, user := range users {
				evalCtx := ffcontext.NewEvaluationContextBuilder(key).
					AddCustom("plan", plan).AddCustom(ownedUserKey, user).Build()

				gotValue, got := viaSegment.Value("flag", evalCtx, flagdef.Context{Service: svc, Segments: provider})
				wantValue, want := viaList.Value("flag", evalCtx, flagdef.Context{Service: svc})

				same := gotValue == wantValue && got.Variant == want.Variant && got.Reason == want.Reason &&
					got.ErrorCode == want.ErrorCode && got.ErrorMessage == want.ErrorMessage
				if !same {
					assert.Equal(
						t,
						wantValue,
						gotValue,
						"%s: key %s plan %s user %v",
						render(rule, nil),
						key,
						plan,
						user,
					)
					assert.Equal(
						t,
						want.Variant,
						got.Variant,
						"%s: key %s plan %s user %v",
						render(rule, nil),
						key,
						plan,
						user,
					)

					return
				}
			}
		}
	}
}

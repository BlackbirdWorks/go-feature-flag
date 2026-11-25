package flagdef

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/testhelpers/clocktest"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

// the details handed to the caller belong to the evaluation: editing the metadata or the rule name
// must not change the next evaluations (the scheduled flag is memoized and shared).
func TestInternalFlag_ScheduledSteps_ReturnedDetailsAreNotShared(t *testing.T) {
	t.Parallel()

	metadata := func() *map[string]any {
		return &map[string]any{
			"release": map[string]any{"owner": "team", tagsName: []any{"a", map[string]any{"k": "v"}}},
		}
	}

	tests := []struct {
		name string
		flag func(base time.Time) *InternalFlag
		// wantRuleName is the name of the rule reported by the second evaluation, empty when none matched
		wantRuleName string
	}{
		{
			name:         "rule matched",
			wantRuleName: "rule",
			flag: func(base time.Time) *InternalFlag {
				return &InternalFlag{
					Metadata:   metadata(),
					Variations: &map[string]*any{"A": new(any("a"))},
					Rules: &[]Rule{{
						Name: new("rule"), Query: new(`key eq "user"`),
						VariationResult: new("A"),
					}},
					DefaultRule: &Rule{VariationResult: new("A")},
					Scheduled: &[]ScheduledStep{{
						Date:    new(base.Add(-time.Hour)),
						Version: new("v2"),
					}},
				}
			},
		},
		{
			name:         "rule selection error",
			wantRuleName: "",
			flag: func(base time.Time) *InternalFlag {
				return &InternalFlag{
					Metadata:   metadata(),
					Variations: &map[string]*any{"A": new(any("a"))},
					// no default rule: the selection of the variation fails
					Scheduled: &[]ScheduledStep{{
						Date:    new(base.Add(-time.Hour)),
						Version: new("v2"),
					}},
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			flg := tt.flag(clocktest.Instant())
			ctx := ffcontext.NewEvaluationContextBuilder(fixtures.ContextKindUser).Build()
			_, first := flg.Value(fixtures.FlagKeyBare, ctx, Context{Service: NewService()})
			rel, ok := first.Metadata["release"].(map[string]any)
			require.True(t, ok)

			rel["owner"] = changedName
			tags, ok := rel[tagsName].([]any)
			require.True(t, ok)

			tags[0] = changedName
			tagMap, ok := tags[1].(map[string]any)
			require.True(t, ok)

			tagMap["k"] = changedName

			if first.RuleName != nil {
				*first.RuleName = changedName
			}

			_, second := flg.Value(fixtures.FlagKeyBare, ctx, Context{Service: NewService()})
			assert.Equal(t, *metadata(), withoutRuleName(second.Metadata))

			gotRuleName := ""
			if second.RuleName != nil {
				gotRuleName = *second.RuleName
			}

			assert.Equal(t, tt.wantRuleName, gotRuleName)
		})
	}
}

func withoutRuleName(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if k != evaluatedRuleNameKey {
			out[k] = v
		}
	}

	return out
}

package flag

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/testutils/clocktest"
)

// the details handed to the caller belong to the evaluation: editing the metadata or the rule name
// must not change the next evaluations (the scheduled flag is memoized and shared).
func TestInternalFlag_ScheduledSteps_ReturnedDetailsAreNotShared(t *testing.T) {
	t.Parallel()
	metadata := func() *map[string]any {
		return &map[string]any{
			"release": map[string]any{"owner": "team", "tags": []any{"a", map[string]any{"k": "v"}}},
		}
	}
	tests := []struct {
		name string
		flag func(base time.Time) *InternalFlag
	}{
		{
			name: "rule matched",
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
			name: "rule selection error",
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
			f := tt.flag(clocktest.Instant())
			ctx := ffcontext.NewEvaluationContextBuilder("user").Build()
			_, first := f.Value("flag", ctx, Context{Service: NewService()})
			rel := first.Metadata["release"].(map[string]any)
			rel["owner"] = "changed"
			rel["tags"].([]any)[0] = "changed"
			rel["tags"].([]any)[1].(map[string]any)["k"] = "changed"
			if first.RuleName != nil {
				*first.RuleName = "changed"
			}
			_, second := f.Value("flag", ctx, Context{Service: NewService()})
			assert.Equal(t, *metadata(), withoutRuleName(second.Metadata))
			if second.RuleName != nil {
				assert.Equal(t, "rule", *second.RuleName)
			}
		})
	}
}

func withoutRuleName(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if k != "evaluatedRuleName" {
			out[k] = v
		}
	}

	return out
}

package flag

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thomaspoignant/go-feature-flag/modules/core/testutils/testconvert"
)

// stagedRolloutFlag: step 1 adds the rule "r", step 2 then step 3 change it.
func stagedRolloutFlag(base time.Time, progressive bool) *InternalFlag {
	rule := func(update Rule) Rule {
		update.Name = testconvert.String("r")
		return update
	}
	step := func(h int, rules ...Rule) ScheduledStep {
		return ScheduledStep{
			Date:         testconvert.Time(base.Add(time.Duration(h) * time.Hour)),
			InternalFlag: InternalFlag{Rules: &rules},
		}
	}
	var steps []ScheduledStep
	if progressive {
		pr := func(initial, end float64) Rule {
			return rule(Rule{ProgressiveRollout: &ProgressiveRollout{
				Initial: &ProgressiveRolloutStep{Percentage: testconvert.Float64(initial)},
				End:     &ProgressiveRolloutStep{Percentage: testconvert.Float64(end)},
			}})
		}
		steps = []ScheduledStep{step(1, pr(1, 2)), step(2, pr(10, 20)), step(3, pr(30, 40))}
	} else {
		pc := func(a, b float64) Rule {
			return rule(Rule{Percentages: &map[string]float64{"A": a, "B": b}})
		}
		steps = []ScheduledStep{step(1, pc(50, 50)), step(2, pc(100, 0)), step(3, pc(0, 100))}
	}
	return &InternalFlag{
		Variations: &map[string]*any{
			"A": testconvert.Interface("a"), "B": testconvert.Interface("b"),
		},
		DefaultRule: &Rule{VariationResult: testconvert.String("A")},
		Scheduled:   &steps,
	}
}

// a stage built from the steps must not be changed by the stages that follow it (they are
// memoized), and the steps themselves must stay as configured.
func TestInternalFlag_ScheduledSteps_StagesDoNotShareRules(t *testing.T) {
	t.Parallel()
	for _, progressive := range []bool{false, true} {
		name := "percentages"
		if progressive {
			name = "progressive rollout"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			base := time.Now()
			f := stagedRolloutFlag(base, progressive)
			snapshot := stagedRolloutFlag(base, progressive)
			// stages 2, 3, 2 (also 1 then 3 then 1), on the same loaded flag
			for _, due := range []int{2, 3, 2, 1, 3, 1, 3, 2} {
				got, err := f.applyScheduledRolloutSteps(base.Add(time.Duration(due)*time.Hour + time.Minute))
				require.NoError(t, err)
				want, err := stagedRolloutFlag(base, progressive).buildScheduledFlag(base.Add(time.Duration(due)*time.Hour + time.Minute))
				require.NoError(t, err)
				require.Len(t, got.GetRules(), 1)
				r, w := got.GetRules()[0], want.GetRules()[0]
				if progressive {
					assert.Equal(t, w.GetProgressiveRollout().Initial.getPercentage(),
						r.GetProgressiveRollout().Initial.getPercentage(), "due=%d initial", due)
					assert.Equal(t, w.GetProgressiveRollout().End.getPercentage(),
						r.GetProgressiveRollout().End.getPercentage(), "due=%d end", due)
				} else {
					assert.Equal(t, w.GetPercentages(), r.GetPercentages(), "due=%d", due)
				}
				assert.Equal(t, *snapshot.Scheduled, *f.Scheduled, "the steps are never modified")
			}
		})
	}
}

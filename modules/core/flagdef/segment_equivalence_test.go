package flagdef_test

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
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

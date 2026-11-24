package flag

import (
	"testing"
	"time"

	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/testutils/testconvert"
	"github.com/stretchr/testify/assert"
)

// the currentDateTime set by the evaluation context enrichment is the date of the evaluation for
// the scheduled steps and the experimentation, as it is when the caller sets it in its context.
func TestInternalFlag_Value_EnrichedCurrentDateTime(t *testing.T) {
	t.Parallel()
	now := time.Now()
	enrichmentAt := func(d time.Time) map[string]any {
		return map[string]any{"gofeatureflag": map[string]any{"currentDateTime": d.Format(time.RFC3339Nano)}}
	}
	scheduled := func() *InternalFlag {
		return &InternalFlag{
			Variations:  &map[string]*any{"A": testconvert.Interface("a"), "B": testconvert.Interface("b")},
			DefaultRule: &Rule{VariationResult: testconvert.String("A")},
			Scheduled: &[]ScheduledStep{{
				Date: testconvert.Time(now.Add(24 * time.Hour)),
				InternalFlag: InternalFlag{
					DefaultRule: &Rule{VariationResult: testconvert.String("B")}},
			}},
		}
	}
	experimentation := func() *InternalFlag {
		return &InternalFlag{
			Variations:      &map[string]*any{"A": testconvert.Interface("a")},
			DefaultRule:     &Rule{VariationResult: testconvert.String("A")},
			Experimentation: &ExperimentationRollout{End: testconvert.Time(now.Add(24 * time.Hour))},
		}
	}
	tests := []struct {
		name        string
		flag        *InternalFlag
		enrichment  map[string]any
		callerDate  *time.Time
		wantVariant string
	}{
		{name: "scheduled step due at the enriched date", flag: scheduled(),
			enrichment: enrichmentAt(now.Add(7 * 24 * time.Hour)), wantVariant: "B"},
		{name: "scheduled step not due at the enriched date", flag: scheduled(),
			enrichment:  enrichmentAt(now.Add(-time.Hour)),
			callerDate:  testconvert.Time(now.Add(7 * 24 * time.Hour)),
			wantVariant: "A"},
		{name: "experimentation over at the enriched date", flag: experimentation(),
			enrichment: enrichmentAt(now.Add(7 * 24 * time.Hour)), wantVariant: VariationSDKDefault},
		{name: "experimentation running at the enriched date", flag: experimentation(),
			enrichment:  enrichmentAt(now),
			callerDate:  testconvert.Time(now.Add(7 * 24 * time.Hour)),
			wantVariant: "A"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := ffcontext.NewEvaluationContextBuilder("user").Build()
			if tt.callerDate != nil {
				ctx.AddCustomAttribute("gofeatureflag", map[string]any{
					"currentDateTime": tt.callerDate.Format(time.RFC3339Nano)})
			}
			_, d := tt.flag.Value("flag", ctx, Context{EvaluationContextEnrichment: tt.enrichment})
			assert.Equal(t, tt.wantVariant, d.Variant)
			// the context of the caller is not modified
			if tt.callerDate == nil {
				assert.NotContains(t, ctx.GetCustom(), "gofeatureflag")
			}
		})
	}
}

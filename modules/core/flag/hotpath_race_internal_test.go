package flag

import (
	"maps"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/testutils/clocktest"
)

func progressiveFlag(endPercentage *float64) *InternalFlag {
	return &InternalFlag{
		Variations: &map[string]*any{
			"A": new(any(true)),
			"B": new(any(false)),
		},
		DefaultRule: &Rule{
			ProgressiveRollout: &ProgressiveRollout{
				Initial: &ProgressiveRolloutStep{
					Variation:  new("B"),
					Percentage: new(float64(0)),
					Date:       new(clocktest.Instant().Add(-time.Hour)),
				},
				End: &ProgressiveRolloutStep{
					Variation:  new("A"),
					Percentage: endPercentage,
					Date:       new(clocktest.Instant().Add(time.Hour)),
				},
			},
		},
	}
}

// runParallel runs fn from 8 goroutines, to be used with -race.
func runParallel(n int, fn func(i int)) {
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for i := range n {
				fn(i)
			}
		})
	}

	wg.Wait()
}

// The evaluation of a flag must never write into the flag, which is shared by all the evaluations.
func TestInternalFlag_Value_DoesNotWriteTheSharedFlag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		endPercentage *float64
		// wantEndPercentage is the end percentage the flag still has after the evaluations
		wantEndPercentage *float64
		name              string
	}{
		{name: "end percentage not set", endPercentage: nil, wantEndPercentage: nil},
		{name: "end percentage 0", endPercentage: new(float64(0)), wantEndPercentage: new(float64(0))},
		{name: "end percentage above 100", endPercentage: new(float64(250)), wantEndPercentage: new(float64(250))},
		{name: "end percentage 100", endPercentage: new(float64(100)), wantEndPercentage: new(float64(100))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			targetFlag := progressiveFlag(tt.endPercentage)

			runParallel(300, func(_ int) {
				ctx := ffcontext.NewEvaluationContext("user")
				_, d := targetFlag.Value("flag", ctx, Context{Service: NewService()})
				assert.Empty(t, d.ErrorCode)
			})
			assert.Equal(t, tt.wantEndPercentage, targetFlag.DefaultRule.ProgressiveRollout.End.Percentage,
				"the end percentage of the flag must not be modified by the evaluations")
		})
	}
}

// The evaluation context of the caller must not be modified by the enrichment: it can be shared
// by concurrent evaluations, and its custom attributes can be nil.
func TestInternalFlag_Value_DoesNotModifyTheEvaluationContext(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		ctxCustom  map[string]any
		enrichment map[string]any
		query      string
		wantMatch  bool
	}{
		{
			name:       "enrichment attribute is used by the query",
			ctxCustom:  map[string]any{"plan": "free"},
			enrichment: map[string]any{"env": "prod"},
			query:      `env eq "prod"`,
			wantMatch:  true,
		},
		{
			name:       "enrichment overrides the attribute of the context",
			ctxCustom:  map[string]any{"env": "dev"},
			enrichment: map[string]any{"env": "prod"},
			query:      `env eq "prod"`,
			wantMatch:  true,
		},
		{
			name:       "context attribute is kept",
			ctxCustom:  map[string]any{"plan": "free"},
			enrichment: map[string]any{"env": "prod"},
			query:      `plan eq "free"`,
			wantMatch:  true,
		},
		{
			name:       "nil custom attributes do not panic",
			ctxCustom:  nil,
			enrichment: map[string]any{"env": "prod"},
			query:      `env eq "prod"`,
			wantMatch:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			targetFlag := &InternalFlag{
				Variations: &map[string]*any{
					"A": new(any(true)),
					"B": new(any(false)),
				},
				Rules:       &[]Rule{{Query: new(tt.query), VariationResult: new("A")}},
				DefaultRule: &Rule{VariationResult: new("B")},
			}
			svc := NewService()
			require.NoError(t, targetFlag.IsValid(svc))

			ctx := &customContext{key: "user", custom: tt.ctxCustom}

			var before map[string]any
			if tt.ctxCustom != nil {
				before = map[string]any{}
				maps.Copy(before, tt.ctxCustom)
			}

			runParallel(200, func(_ int) {
				v, _ := targetFlag.Value("flag", ctx, Context{EvaluationContextEnrichment: tt.enrichment, Service: svc})
				assert.Equal(t, tt.wantMatch, v == true)
			})
			assert.Equal(t, before, ctx.custom, "the evaluation context must not be modified")
		})
	}
}

// customContext is a ffcontext.Context that exposes the map it was built with, as is.
type customContext struct {
	custom map[string]any
	key    string
}

func (c *customContext) GetKey() string            { return c.key }
func (c *customContext) IsAnonymous() bool         { return false }
func (c *customContext) GetCustom() map[string]any { return c.custom }
func (c *customContext) AddCustomAttribute(n string, v any) {
	if c.custom != nil {
		c.custom[n] = v
	}
}
func (c *customContext) ExtractGOFFProtectedFields() ffcontext.GoffContextSpecifics {
	return ffcontext.GoffContextSpecifics{}
}

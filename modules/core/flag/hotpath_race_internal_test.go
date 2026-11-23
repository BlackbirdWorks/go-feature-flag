package flag

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thomaspoignant/go-feature-flag/modules/core/ffcontext"
	"github.com/thomaspoignant/go-feature-flag/modules/core/testutils/testconvert"
)

func progressiveFlag(endPercentage *float64) *InternalFlag {
	return &InternalFlag{
		Variations: &map[string]*any{
			"A": testconvert.Interface(true),
			"B": testconvert.Interface(false),
		},
		DefaultRule: &Rule{
			ProgressiveRollout: &ProgressiveRollout{
				Initial: &ProgressiveRolloutStep{
					Variation:  testconvert.String("B"),
					Percentage: testconvert.Float64(0),
					Date:       testconvert.Time(time.Now().Add(-time.Hour)),
				},
				End: &ProgressiveRolloutStep{
					Variation:  testconvert.String("A"),
					Percentage: endPercentage,
					Date:       testconvert.Time(time.Now().Add(time.Hour)),
				},
			},
		},
	}
}

// runParallel runs fn from 8 goroutines, to be used with -race.
func runParallel(n int, fn func(i int)) {
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < n; i++ {
				fn(i)
			}
		}()
	}
	wg.Wait()
}

// The evaluation of a flag must never write into the flag, which is shared by all the evaluations.
func TestInternalFlag_Value_DoesNotWriteTheSharedFlag(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		endPercentage *float64
	}{
		{name: "end percentage not set", endPercentage: nil},
		{name: "end percentage 0", endPercentage: testconvert.Float64(0)},
		{name: "end percentage above 100", endPercentage: testconvert.Float64(250)},
		{name: "end percentage 100", endPercentage: testconvert.Float64(100)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := progressiveFlag(tt.endPercentage)
			runParallel(300, func(i int) {
				ctx := ffcontext.NewEvaluationContext("user")
				_, d := f.Value("flag", ctx, Context{})
				assert.Empty(t, d.ErrorCode)
			})
			assert.Equal(t, tt.endPercentage, f.DefaultRule.ProgressiveRollout.End.Percentage,
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
			f := &InternalFlag{
				Variations: &map[string]*any{
					"A": testconvert.Interface(true),
					"B": testconvert.Interface(false),
				},
				Rules:       &[]Rule{{Query: testconvert.String(tt.query), VariationResult: testconvert.String("A")}},
				DefaultRule: &Rule{VariationResult: testconvert.String("B")},
			}
			require.NoError(t, f.IsValid())
			ctx := &customContext{key: "user", custom: tt.ctxCustom}
			var before map[string]any
			if tt.ctxCustom != nil {
				before = map[string]any{}
				for k, v := range tt.ctxCustom {
					before[k] = v
				}
			}
			runParallel(200, func(i int) {
				v, _ := f.Value("flag", ctx, Context{EvaluationContextEnrichment: tt.enrichment})
				assert.Equal(t, tt.wantMatch, v == true)
			})
			assert.Equal(t, before, ctx.custom, "the evaluation context must not be modified")
		})
	}
}

// customContext is a ffcontext.Context that exposes the map it was built with, as is.
type customContext struct {
	key    string
	custom map[string]any
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

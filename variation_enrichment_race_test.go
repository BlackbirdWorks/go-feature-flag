package ffclient_test

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	ffclient "github.com/thomaspoignant/go-feature-flag"
	"github.com/thomaspoignant/go-feature-flag/ffcontext"
	"github.com/thomaspoignant/go-feature-flag/retriever/fileretriever"
)

// the evaluation context enrichment (and the environment) is shared by all the evaluations:
// evaluating concurrently must not write it, nor modify the evaluation context of the callers.
func Test_EvaluationContextEnrichment_ConcurrentEvaluations(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		environment string
		enrichment  map[string]any
	}{
		{name: "enrichment and environment", environment: "prod", enrichment: map[string]any{"team": "a"}},
		{name: "environment only", environment: "prod"},
		{name: "enrichment only", enrichment: map[string]any{"team": "a"}},
		{name: "nothing"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c, err := ffclient.New(ffclient.Config{
				PollingInterval:             time.Hour,
				Retriever:                   &fileretriever.Retriever{Path: "testdata/flag-config.yaml"},
				Environment:                 tt.environment,
				EvaluationContextEnrichment: tt.enrichment,
			})
			require.NoError(t, err)
			defer c.Close()

			shared := ffcontext.NewEvaluationContextBuilder("user").AddCustom("plan", "free").Build()
			var wg sync.WaitGroup
			for w := 0; w < 8; w++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for i := 0; i < 100; i++ {
						_, _ = c.BoolVariation("test-flag", shared, false)
						_ = c.AllFlagsState(shared)
					}
				}()
			}
			wg.Wait()
			assert.Equal(t, map[string]any{"plan": "free"}, shared.GetCustom(),
				"the evaluation context of the caller must not be modified")
			assert.Equal(t, len(tt.enrichment), len(c.GetEvaluationContextEnrichment()),
				"the configured enrichment must not be modified")
		})
	}
}

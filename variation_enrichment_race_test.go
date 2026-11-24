package ffclient_test

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ffclient "github.com/blackbirdworks/semaphore"
	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/retriever/fileretriever"
)

// the evaluation context enrichment (and the environment) is shared by all the evaluations:
// evaluating concurrently must not write it, nor modify the evaluation context of the callers.
func Test_EvaluationContextEnrichment_ConcurrentEvaluations(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		environment string
		enrichment  map[string]any
		// wantCustom is the custom attributes of the evaluation context of the callers afterwards and
		// wantEnrichment the configured enrichment afterwards: neither is modified
		wantCustom     map[string]any
		wantEnrichment map[string]any
	}{
		{
			name: "enrichment and environment", environment: "prod", enrichment: map[string]any{"team": "a"},
			wantCustom: map[string]any{"plan": "free"}, wantEnrichment: map[string]any{"team": "a"},
		},
		{name: "environment only", environment: "prod", wantCustom: map[string]any{"plan": "free"}},
		{
			name: "enrichment only", enrichment: map[string]any{"team": "a"},
			wantCustom: map[string]any{"plan": "free"}, wantEnrichment: map[string]any{"team": "a"},
		},
		{name: "nothing", wantCustom: map[string]any{"plan": "free"}},
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
			for range 8 {
				wg.Go(func() {
					for range 100 {
						_, _ = c.BoolVariation("test-flag", shared, false)
						_ = c.AllFlagsState(shared)
					}
				})
			}
			wg.Wait()
			assert.Equal(t, tt.wantCustom, shared.GetCustom(),
				"the evaluation context of the caller must not be modified")
			assert.Len(t, c.GetEvaluationContextEnrichment(), len(tt.wantEnrichment),
				"the configured enrichment must not be modified")
		})
	}
}

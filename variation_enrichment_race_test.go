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
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

const (
	team = "team"
)

// the evaluation context enrichment (and the environment) is shared by all the evaluations:
// evaluating concurrently must not write it, nor modify the evaluation context of the callers.
func Test_EvaluationContextEnrichment_ConcurrentEvaluations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		enrichment map[string]any
		// wantCustom is the custom attributes of the evaluation context of the callers afterwards and
		// wantEnrichment the configured enrichment afterwards: neither is modified
		wantCustom     map[string]any
		wantEnrichment map[string]any
		name           string
		environment    string
	}{
		{
			name: "enrichment and environment", environment: fixtures.Prod, enrichment: map[string]any{team: "a"},
			wantCustom: map[string]any{fixtures.Plan: fixtures.Free}, wantEnrichment: map[string]any{team: "a"},
		},
		{
			name:        "environment only",
			environment: fixtures.Prod,
			wantCustom:  map[string]any{fixtures.Plan: fixtures.Free},
		},
		{
			name: "enrichment only", enrichment: map[string]any{team: "a"},
			wantCustom: map[string]any{fixtures.Plan: fixtures.Free}, wantEnrichment: map[string]any{team: "a"},
		},
		{name: "nothing", wantCustom: map[string]any{fixtures.Plan: fixtures.Free}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client, err := ffclient.New(&ffclient.Config{
				PollingInterval:             time.Hour,
				Retriever:                   &fileretriever.Retriever{Path: fixtures.FlagConfigYAMLPath},
				Environment:                 tt.environment,
				EvaluationContextEnrichment: tt.enrichment,
			})
			require.NoError(t, err)

			defer client.Close()

			shared := ffcontext.NewEvaluationContextBuilder(fixtures.User).
				AddCustom(fixtures.Plan, fixtures.Free).
				Build()

			var wg sync.WaitGroup
			for range 8 {
				wg.Go(func() {
					for range 100 {
						_, _ = client.BoolVariation(fixtures.FlagKeyTest, shared, false)
						_ = client.AllFlagsState(shared)
					}
				})
			}

			wg.Wait()
			assert.Equal(t, tt.wantCustom, shared.GetCustom(),
				"the evaluation context of the caller must not be modified")
			assert.Len(t, client.GetEvaluationContextEnrichment(), len(tt.wantEnrichment),
				"the configured enrichment must not be modified")
		})
	}
}

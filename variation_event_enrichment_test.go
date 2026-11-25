package ffclient_test

import (
	"context"
	"maps"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ffclient "github.com/blackbirdworks/semaphore"
	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/retriever/fileretriever"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
	"github.com/blackbirdworks/semaphore/utils/fflog"
)

const (
	regionName = "region"
)

type collectingExporter struct {
	events []exporter.FeatureEvent
	mu     sync.Mutex
}

func (*collectingExporter) IsBulk() bool { return true }
func (c *collectingExporter) Export(_ context.Context, _ *fflog.FFLogger, ev []exporter.ExportableEvent) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, e := range ev {
		if fe, ok := e.(exporter.FeatureEvent); ok {
			c.events = append(c.events, fe)
		}
	}

	return nil
}

// The evaluation context enrichment applies to the evaluation events too: the configured exporter
// metadata and anonymous status win over the ones of the caller, which is never modified.
func Test_EvaluationEvents_UseTheEnrichedContext(t *testing.T) {
	t.Parallel()

	meta := func(region string) map[string]any {
		return map[string]any{
			fixtures.GoFeatureFlagOrg: map[string]any{"exporterMetadata": map[string]any{regionName: region}},
		}
	}

	tests := []struct {
		name         string
		enrichment   map[string]any
		callerCustom map[string]any
		wantMetadata map[string]any
		wantKind     string
	}{
		{
			name:         "no enrichment",
			callerCustom: meta("us"),
			wantMetadata: map[string]any{regionName: "us"},
			wantKind:     fixtures.User,
		},
		{
			name:         "enriched metadata, caller has none",
			enrichment:   meta("eu"),
			wantMetadata: map[string]any{regionName: "eu"},
			wantKind:     fixtures.User,
		},
		{name: "enriched metadata wins over the caller's", enrichment: meta("eu"), callerCustom: meta("us"),
			wantMetadata: map[string]any{regionName: "eu"}, wantKind: fixtures.User},
		{name: "enriched anonymous", enrichment: map[string]any{fixtures.Anonymous: true}, wantKind: "anonymousUser"},
		{name: "unrelated enrichment changes nothing", enrichment: map[string]any{"env": fixtures.Prod},
			callerCustom: meta("us"), wantMetadata: map[string]any{regionName: "us"}, wantKind: fixtures.User},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			exp := &collectingExporter{}
			client, err := ffclient.New(&ffclient.Config{
				PollingInterval:             time.Hour,
				Retriever:                   &fileretriever.Retriever{Path: fixtures.FlagConfigYAMLPath},
				EvaluationContextEnrichment: tt.enrichment,
				DataExporter:                ffclient.DataExporter{Exporter: exp, FlushInterval: time.Hour},
			})
			require.NoError(t, err)

			ctx := ffcontext.NewEvaluationContext(fixtures.User)
			for k, v := range tt.callerCustom {
				ctx.AddCustomAttribute(k, v)
			}

			_, err = client.BoolVariation(fixtures.FlagKeyTest, ctx, false)
			require.NoError(t, err)
			client.Close()

			exp.mu.Lock()
			defer exp.mu.Unlock()

			require.Len(t, exp.events, 1)
			assert.Equal(t, tt.wantKind, exp.events[0].ContextKind)

			if tt.wantMetadata == nil {
				assert.Empty(t, exp.events[0].Metadata)
			} else {
				assert.Equal(t, tt.wantMetadata, exp.events[0].Metadata)
			}

			wantCustom := map[string]any{}
			maps.Copy(wantCustom, tt.callerCustom)
			assert.Equal(t, wantCustom, ctx.GetCustom(), "the caller's context is not modified")
		})
	}
}

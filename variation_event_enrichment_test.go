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
	"github.com/blackbirdworks/semaphore/utils/fflog"
)

type collectingExporter struct {
	mu     sync.Mutex
	events []exporter.FeatureEvent
}

func (c *collectingExporter) IsBulk() bool { return true }
func (c *collectingExporter) Export(_ context.Context, _ *fflog.FFLogger, ev []exporter.ExportableEvent) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range ev {
		c.events = append(c.events, e.(exporter.FeatureEvent))
	}

	return nil
}

// The evaluation context enrichment applies to the evaluation events too: the configured exporter
// metadata and anonymous status win over the ones of the caller, which is never modified.
func Test_EvaluationEvents_UseTheEnrichedContext(t *testing.T) {
	t.Parallel()
	meta := func(region string) map[string]any {
		return map[string]any{"gofeatureflag": map[string]any{"exporterMetadata": map[string]any{"region": region}}}
	}
	tests := []struct {
		name         string
		enrichment   map[string]any
		callerCustom map[string]any
		wantMetadata map[string]any
		wantKind     string
	}{
		{name: "no enrichment", callerCustom: meta("us"), wantMetadata: map[string]any{"region": "us"}, wantKind: "user"},
		{name: "enriched metadata, caller has none", enrichment: meta("eu"), wantMetadata: map[string]any{"region": "eu"}, wantKind: "user"},
		{name: "enriched metadata wins over the caller's", enrichment: meta("eu"), callerCustom: meta("us"),
			wantMetadata: map[string]any{"region": "eu"}, wantKind: "user"},
		{name: "enriched anonymous", enrichment: map[string]any{"anonymous": true}, wantKind: "anonymousUser"},
		{name: "unrelated enrichment changes nothing", enrichment: map[string]any{"env": "prod"},
			callerCustom: meta("us"), wantMetadata: map[string]any{"region": "us"}, wantKind: "user"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			exp := &collectingExporter{}
			c, err := ffclient.New(ffclient.Config{
				PollingInterval:             time.Hour,
				Retriever:                   &fileretriever.Retriever{Path: "testdata/flag-config.yaml"},
				EvaluationContextEnrichment: tt.enrichment,
				DataExporter:                ffclient.DataExporter{Exporter: exp, FlushInterval: time.Hour},
			})
			require.NoError(t, err)
			ctx := ffcontext.NewEvaluationContext("user")
			for k, v := range tt.callerCustom {
				ctx.AddCustomAttribute(k, v)
			}
			_, err = c.BoolVariation("test-flag", ctx, false)
			require.NoError(t, err)
			c.Close()

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

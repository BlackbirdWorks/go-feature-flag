package ffclient_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ffclient "github.com/blackbirdworks/semaphore"
	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/retriever/fileretriever"
	"github.com/blackbirdworks/semaphore/testutils/mock"
)

const (
	anonymous2         = "anonymous"
	fixture1668D845051 = "1668d845-051d-4dd9-907a-7ebe6aa2c9da"
	value              = "value"
)

func TestValidTrackingEvent(t *testing.T) {
	t.Parallel()

	exp := mock.TrackingEventExporter{Bulk: false}
	goff, err := ffclient.New(ffclient.Config{
		PollingInterval: 500 * time.Millisecond,
		Retriever:       &fileretriever.Retriever{Path: "./testdata/flag-config.yaml"},
		DataExporters: []ffclient.DataExporter{
			{
				FlushInterval:     100 * time.Millisecond,
				MaxEventInMemory:  100,
				Exporter:          &exp,
				ExporterEventType: ffclient.TrackingEventExporter,
			},
		},
	})
	require.NoError(t, err)

	goff.Track(
		"my-feature-flag",
		ffcontext.NewEvaluationContextBuilder(fixture1668D845051).
			AddCustom("admin", true).
			AddCustom(anonymous2, true).
			Build(),
		map[string]any{"additional data": value},
	)

	assert.Len(t, exp.ExportedEvents, 1)
	assert.Equal(t, fixture1668D845051, exp.ExportedEvents[0].UserKey)
	assert.Equal(t, "my-feature-flag", exp.ExportedEvents[0].Key)
	assert.Equal(
		t,
		map[string]any{
			"targetingKey": fixture1668D845051,
			"admin":        true,
			anonymous2:     true,
		},
		exp.ExportedEvents[0].EvaluationContext,
	)
	assert.Equal(
		t,
		map[string]any{"additional data": value},
		exp.ExportedEvents[0].TrackingDetails,
	)
}

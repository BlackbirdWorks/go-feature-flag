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

func TestValidTrackingEvent(t *testing.T) {
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
		ffcontext.NewEvaluationContextBuilder("1668d845-051d-4dd9-907a-7ebe6aa2c9da").
			AddCustom("admin", true).
			AddCustom("anonymous", true).
			Build(),
		map[string]any{"additional data": "value"},
	)

	assert.Len(t, exp.ExportedEvents, 1)
	assert.Equal(t, "1668d845-051d-4dd9-907a-7ebe6aa2c9da", exp.ExportedEvents[0].UserKey)
	assert.Equal(t, "my-feature-flag", exp.ExportedEvents[0].Key)
	assert.Equal(
		t,
		map[string]any{
			"targetingKey": "1668d845-051d-4dd9-907a-7ebe6aa2c9da",
			"admin":        true,
			"anonymous":    true,
		},
		exp.ExportedEvents[0].EvaluationContext,
	)
	assert.Equal(
		t,
		map[string]any{"additional data": "value"},
		exp.ExportedEvents[0].TrackingDetails,
	)
}

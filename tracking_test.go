package ffclient_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ffclient "github.com/blackbirdworks/semaphore"
	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/retriever/fileretriever"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
	"github.com/blackbirdworks/semaphore/testhelpers/mock"
)

const (
	evalContextUUID = "1668d845-051d-4dd9-907a-7ebe6aa2c9da"
)

func TestValidTrackingEvent(t *testing.T) {
	t.Parallel()

	exp := mock.TrackingEventExporter{Bulk: false}
	goff, err := ffclient.New(&ffclient.Config{
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
		ffcontext.NewEvaluationContextBuilder(evalContextUUID).
			AddCustom("admin", true).
			AddCustom(fixtures.Anonymous, true).
			Build(),
		map[string]any{"additional data": fixtures.Value},
	)

	assert.Len(t, exp.ExportedEvents, 1)
	assert.Equal(t, evalContextUUID, exp.ExportedEvents[0].UserKey)
	assert.Equal(t, "my-feature-flag", exp.ExportedEvents[0].Key)
	assert.Equal(
		t,
		map[string]any{
			"targetingKey":     evalContextUUID,
			"admin":            true,
			fixtures.Anonymous: true,
		},
		exp.ExportedEvents[0].EvaluationContext,
	)
	assert.Equal(
		t,
		map[string]any{"additional data": fixtures.Value},
		exp.ExportedEvents[0].TrackingDetails,
	)
}

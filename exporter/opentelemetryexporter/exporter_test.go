package opentelemetryexporter

import (
	"context"
	"testing"
	"text/template"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/utils/fflog"
)

// MockUnsupportedEvent is used to test the '!ok' type assertion branch.
type MockUnsupportedEvent struct{}

func (m MockUnsupportedEvent) FormatInCSV(_ *template.Template) ([]byte, error) { return nil, nil }
func (m MockUnsupportedEvent) FormatInJSON() ([]byte, error)                    { return nil, nil }
func (m MockUnsupportedEvent) GetCreationDate() int64                           { return 0 }
func (m MockUnsupportedEvent) GetUserKey() string                               { return "" }
func (m MockUnsupportedEvent) GetKey() string                                   { return "" }

func TestOpenTelemetryIsBulk(t *testing.T) {
	t.Parallel()

	exp := Exporter{}
	assert.False(t, exp.IsBulk(), "OpenTelemetry exporter is not a bulk exporter")
}

func TestExporterCreatesOneSpanPerEvent(t *testing.T) {
	t.Parallel()

	// Arrange
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(recorder),
	)

	exp := &Exporter{
		TracerName:     "test-tracer",
		tracerProvider: tp,
	}

	events := []exporter.ExportableEvent{
		exporter.FeatureEvent{
			Kind:         "feature",
			ContextKind:  "user",
			UserKey:      "user-1",
			CreationDate: 1617970547,
			Key:          "flag-1",
			Variation:    "A",
			Value:        true,
			Default:      false,
			Version:      "v1",
			Source:       "SERVER",
		},
		exporter.FeatureEvent{
			Kind:         "feature",
			ContextKind:  "anonymousUser",
			UserKey:      "user-2",
			CreationDate: 1617970548,
			Key:          "flag-2",
			Variation:    "B",
			Value:        "on",
			Default:      true,
			Version:      "v2",
			Source:       "PROVIDER_CACHE",
		},
	}

	logger := &fflog.FFLogger{}

	// Act
	err := exp.Export(context.Background(), logger, events)

	// Assert
	require.NoError(t, err)

	spans := recorder.Ended()
	assert.Len(t, spans, 2, "should create one span per feature event")

	assert.Equal(t, spanName, spans[0].Name())
	assert.Equal(t, spanName, spans[1].Name())
}

func TestExporterSpanAttributes(t *testing.T) {
	t.Parallel()

	// Arrange
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(recorder),
	)

	exp := &Exporter{tracerProvider: tp}

	event := exporter.FeatureEvent{
		Kind:         "feature",
		ContextKind:  "user",
		UserKey:      "user-123",
		CreationDate: 1617970547,
		Key:          "my-flag",
		Variation:    "on",
		Value:        true,
		Default:      false,
		Version:      "v1",
		Source:       "SERVER",
		//  the for-loop branch
		Metadata: map[string]any{
			"team": "platform",
		},
	}

	// Act
	err := exp.Export(context.Background(), &fflog.FFLogger{}, []exporter.ExportableEvent{event})

	// Assert
	require.NoError(t, err)

	spans := recorder.Ended()
	assert.Len(t, spans, 1)

	attrs := spans[0].Attributes()

	assertAttribute(t, attrs, "feature_flag.key", "my-flag")
	assertAttribute(t, attrs, "feature_flag.user_key", "user-123")
	assertAttribute(t, attrs, "feature_flag.context_kind", "user")
	assertAttribute(t, attrs, "feature_flag.variation", "on")
	assertAttribute(t, attrs, "feature_flag.version", "v1")
	assertAttribute(t, attrs, "feature_flag.source", "SERVER")
	assertAttribute(t, attrs, "feature_flag.metadata.team", "platform")
}

// 'continue' logic when receiving non-FeatureEvent types.
func TestExporter_Export_IgnoreUnsupportedEventTypes(t *testing.T) {
	t.Parallel()

	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	exp := &Exporter{tracerProvider: tp}

	events := []exporter.ExportableEvent{MockUnsupportedEvent{}}
	err := exp.Export(context.Background(), nil, events)

	require.NoError(t, err)
	assert.Empty(t, recorder.Ended(), "No spans should be created for unsupported events")
}

func assertAttribute(t *testing.T, attrs []attribute.KeyValue, key string, expected any) {
	t.Helper()
	for _, attr := range attrs {
		if string(attr.Key) == key {
			require.Equal(t, expected, attr.Value.AsInterface(), "attribute %s mismatch", key)

			return
		}
	}
	require.Failf(t, "attribute not found", "attribute %s not found", key)
}

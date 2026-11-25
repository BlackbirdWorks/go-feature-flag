package opentelemetryexporter

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/blackbirdworks/semaphore/exporter"
)

const (
	// fixedAttributes is the number of attributes that every event has.
	fixedAttributes = 8
)

var _ exporter.Exporter = &Exporter{}

const (
	defaultTracerName = "semaphore"
	spanName          = "feature_flag.evaluate"
)

// Exporter sends evaluation events as OpenTelemetry logs.
type Exporter struct {
	// tracerProvider is where the tracer comes from (the global OpenTelemetry provider when nil).
	tracerProvider trace.TracerProvider
	tracer         trace.Tracer
	// TracerName allows overriding the OpenTelemetry tracer name.
	// Default: semaphore
	TracerName string
	once       sync.Once
}

// Export sends the events as OpenTelemetry logs.
func (e *Exporter) Export(
	ctx context.Context,
	logger *slog.Logger,
	events []exporter.ExportableEvent,
) error {
	if logger == nil {
		// Noop logger to avoid any logging and nil issues
		logger = slog.New(slog.DiscardHandler)
	}

	e.init()

	for _, evt := range events {
		featureEvent, ok := evt.(exporter.FeatureEvent)
		if !ok {
			continue
		}

		e.createSpan(ctx, featureEvent, logger)
	}

	return nil
}

// IsBulk returns false: this exporter sends each event on its own as an OpenTelemetry log record,
// it does not batch events.
func (*Exporter) IsBulk() bool {
	return false
}

func (e *Exporter) init() {
	e.once.Do(func() {
		if e.TracerName == "" {
			e.TracerName = defaultTracerName
		}

		if e.tracerProvider != nil {
			e.tracer = e.tracerProvider.Tracer(e.TracerName)

			return
		}

		e.tracer = otel.Tracer(e.TracerName)
	})
}

func (e *Exporter) createSpan(ctx context.Context, event exporter.FeatureEvent, logger *slog.Logger) {
	startTime := time.Unix(event.CreationDate, 0)

	_, span := e.tracer.Start(
		ctx,
		spanName,
		trace.WithTimestamp(startTime),
	)
	defer span.End()

	// Collect all attributes into a single slice, then set them once on the span.
	attributes := make([]attribute.KeyValue, 0, fixedAttributes+len(event.Metadata))
	attributes = append(attributes,
		attribute.String("feature_flag.key", event.Key),
		attribute.String("feature_flag.user_key", event.UserKey),
		attribute.String("feature_flag.context_kind", event.ContextKind),
		attribute.String("feature_flag.variation", event.Variation),
		attribute.Bool("feature_flag.default", event.Default),
		attribute.String("feature_flag.version", event.Version),
		attribute.String("feature_flag.source", event.Source),
	)

	logger.DebugContext(ctx, "Exporting feature flag evaluation")

	// Value (safe stringification)
	if event.Value != nil {
		b, err := json.Marshal(event.Value)
		if err != nil {
			if logger != nil {
				logger.DebugContext(ctx, "failed to marshal feature flag value for otel attribute", slog.Any("error", err))
			}
		} else {
			attributes = append(attributes, attribute.String("feature_flag.value", string(b)))
		}
	}

	// Metadata
	for k, val := range event.Metadata {
		key := "feature_flag.metadata." + k

		switch value := val.(type) {
		case bool:
			attributes = append(attributes, attribute.Bool(key, value))
		case int:
			attributes = append(attributes, attribute.Int(key, value))
		case int64:
			attributes = append(attributes, attribute.Int64(key, value))
		case float64:
			attributes = append(attributes, attribute.Float64(key, value))
		case string:
			attributes = append(attributes, attribute.String(key, value))
		default:
			attributes = append(attributes, attribute.String(key, fmt.Sprint(val)))
		}
	}

	logger.DebugContext(ctx, "Setting span attributes for feature flag evaluation")
	// Apply all attributes in a single call to reduce overhead.
	if len(attributes) > 0 {
		span.SetAttributes(attributes...)
	}

	// Status
	if event.Default {
		span.SetStatus(codes.Error, "default value used")
	} else {
		span.SetStatus(codes.Ok, "evaluation successful")
	}

	logger.DebugContext(ctx, "Feature flag evaluation span status set")
}

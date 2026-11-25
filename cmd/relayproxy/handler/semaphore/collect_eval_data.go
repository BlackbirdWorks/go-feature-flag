package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/blackbirdworks/huma-middleware/middleware/logging"
	"github.com/go-viper/mapstructure/v2"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/helper"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/model"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/cmdhelpers/configfile"
	cmdlog "github.com/blackbirdworks/semaphore/cmdhelpers/log"
	"github.com/blackbirdworks/semaphore/exporter"
)

const (
	// maxCreationDateSeconds is the last creation date, in seconds, that is not in milliseconds (year 2286).
	maxCreationDateSeconds = 9999999999
)

// CollectEvalData is the controller of the endpoint that collects the events of the providers.
type CollectEvalData struct {
	flagsetManager service.FlagsetManager
	metrics        metric.Metrics
}

// NewCollectEvalData initialize the controller for the /data/collector endpoint.
func NewCollectEvalData(
	flagsetManager service.FlagsetManager,
	metrics metric.Metrics,
) *CollectEvalData {
	return &CollectEvalData{
		flagsetManager: flagsetManager,
		metrics:        metrics,
	}
}

// CollectEvalDataInput is the request of the data collector endpoint.
type CollectEvalDataInput struct {
	Request

	// RawBody is the JSON document of a model.CollectEvalDataRequest, the handler decodes it.
	RawBody []byte `contentType:"application/json"`
}

// CollectEvalDataOutput is the response of the data collector endpoint.
type CollectEvalDataOutput struct {
	Body model.CollectEvalDataResponse
}

// Handle is the entry point for the data/collector endpoint.
func (h *CollectEvalData) Handle(ctx context.Context, input *CollectEvalDataInput) (*CollectEvalDataOutput, error) {
	ctx, span := otel.Tracer(configfile.OtelTracerName).Start(ctx, "collectEventData")
	defer span.End()

	reqBody := new(model.CollectEvalDataRequest)

	bindErr := helper.BindRawBody(input.ContentType(), input.RawBody, reqBody)
	if bindErr != nil {
		return nil, fmt.Errorf("binding the evaluation data: %w", helper.NewHTTPError(
			http.StatusBadRequest,
			fmt.Sprintf("collectEvalData: invalid input data %v", bindErr)))
	}

	if reqBody.Events == nil {
		return nil, fmt.Errorf(
			"checking the evaluation data: %w",
			helper.NewHTTPError(http.StatusBadRequest, "collectEvalData: invalid input data"),
		)
	}

	span.SetAttributes(attribute.Int("collectEventData.eventCollectionSize", len(reqBody.Events)))

	// The flag set is looked up after the body is read, not taken from the authentication: the events are
	// acknowledged to the flag set, which must not have been closed by a reload while the body was received.
	flagset, httpErr := helper.FlagSet(h.flagsetManager, input.APIKey())
	if httpErr != nil {
		return nil, fmt.Errorf("getting the flag set of the evaluation data: %w", httpErr)
	}

	logger := logging.FromContext(ctx)
	counterTracking := 0
	counterEvaluation := 0

	for i, event := range reqBody.Events {
		// Check if context is cancelled before processing each event, to avoid
		// long delays on large payloads.
		select {
		case <-ctx.Done():
			err := fmt.Errorf("context cancelled after processing %d/%d events: %w",
				i, len(reqBody.Events), ctx.Err())
			span.SetAttributes(
				attribute.Int("processed_tracking", counterTracking),
				attribute.Int("processed_evaluation", counterEvaluation),
				attribute.Int("total_events", len(reqBody.Events)),
			)
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())

			return nil, err
		default:
			// all good, keep going
		}

		switch event["kind"] {
		case "tracking":
			trackingEv, err := convertTrackingEvent(ctx, event, logger)
			if err != nil {
				logger.LogAttrs(ctx, slog.LevelError,
					"impossible to convert the event to a tracking event",
					cmdlog.Err(err),
				)

				continue
			}

			flagset.CollectTrackingEventData(trackingEv)

			counterTracking++
		default:
			featureEv, err := convertFeatureEvent(ctx, event, reqBody.Meta, logger)
			if err != nil {
				logger.LogAttrs(
					ctx,
					slog.LevelError,
					"impossible to convert the event to a feature event",
					cmdlog.Err(err),
				)

				continue
			}

			flagset.CollectEventData(featureEv)

			counterEvaluation++
		}
	}

	span.SetAttributes(attribute.Int("collectEventData.trackingCollectionSize", counterTracking))
	span.SetAttributes(
		attribute.Int("collectEventData.evaluationCollectionSize", counterEvaluation),
	)
	h.metrics.IncCollectEvalData(float64(len(reqBody.Events)))

	return &CollectEvalDataOutput{Body: model.CollectEvalDataResponse{
		IngestedContentCount: len(reqBody.Events),
	}}, nil
}

func convertTrackingEvent(
	ctx context.Context,
	event map[string]any,
	logger *slog.Logger,
) (exporter.TrackingEvent, error) {
	var trackingEv exporter.TrackingEvent

	marshalled, err := json.Marshal(event)
	if err != nil {
		return exporter.TrackingEvent{}, fmt.Errorf("marshal tracking event: %w", err)
	}

	err = json.Unmarshal(marshalled, &trackingEv)
	if err != nil {
		return exporter.TrackingEvent{}, fmt.Errorf("unmarshal tracking event: %w", err)
	}

	trackingEv.CreationDate = formatCreationDate(ctx, trackingEv.CreationDate, logger)

	return trackingEv, nil
}

func convertFeatureEvent(
	ctx context.Context,
	event map[string]any,
	metadata exporter.FeatureEventMetadata,
	logger *slog.Logger) (exporter.FeatureEvent, error) {
	var featureEv exporter.FeatureEvent

	err := mapstructure.Decode(event, &featureEv)
	if err != nil {
		return exporter.FeatureEvent{}, fmt.Errorf("decode feature event: %w", err)
	}

	if featureEv.Source == "" {
		featureEv.Source = "PROVIDER_CACHE"
	}

	if metadata != nil {
		featureEv.Metadata = metadata
	}

	featureEv.CreationDate = formatCreationDate(ctx, featureEv.CreationDate, logger)

	return featureEv, nil
}

func formatCreationDate(ctx context.Context, input int64, logger *slog.Logger) int64 {
	if input > maxCreationDateSeconds {
		logger.LogAttrs(ctx, slog.LevelWarn,
			"creationDate received is in milliseconds, we convert it to seconds",
			slog.Int64("creationDate", input))
		// if we receive a timestamp in milliseconds, we convert it to seconds
		// but since it is totally possible to have a timestamp in seconds that is bigger than 9999999999
		// we will accept timestamp up to 9999999999 (2286-11-20 18:46:39 +0100 CET)
		converted, _ := strconv.ParseInt(
			strconv.FormatInt(input, 10)[:10], 10, 64)

		return converted
	}

	return input
}

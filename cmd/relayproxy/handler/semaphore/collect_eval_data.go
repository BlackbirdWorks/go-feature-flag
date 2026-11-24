package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

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
	logger         *slog.Logger
	metrics        metric.Metrics
}

// NewCollectEvalData initialize the controller for the /data/collector endpoint.
func NewCollectEvalData(
	flagsetManager service.FlagsetManager,
	metrics metric.Metrics,
	logger *slog.Logger,
) *CollectEvalData {
	return &CollectEvalData{
		flagsetManager: flagsetManager,
		metrics:        metrics,
		logger:         logger,
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
func (h *CollectEvalData) Handle(ctx context.Context, in *CollectEvalDataInput) (*CollectEvalDataOutput, error) {
	ctx, span := otel.Tracer(configfile.OtelTracerName).Start(ctx, "collectEventData")
	defer span.End()

	reqBody := new(model.CollectEvalDataRequest)
	if err := helper.BindRawBody(in.ContentType(), in.RawBody, reqBody); err != nil {
		return nil, helper.NewHTTPError(
			http.StatusBadRequest,
			fmt.Sprintf("collectEvalData: invalid input data %v", err))
	}
	if reqBody.Events == nil {
		return nil, helper.NewHTTPError(http.StatusBadRequest, "collectEvalData: invalid input data")
	}

	span.SetAttributes(attribute.Int("collectEventData.eventCollectionSize", len(reqBody.Events)))

	// The flag set is looked up after the body is read, not taken from the authentication: the events are
	// acknowledged to the flag set, which must not have been closed by a reload while the body was received.
	flagset, httpErr := helper.FlagSet(h.flagsetManager, in.APIKey())
	if httpErr != nil {
		return nil, httpErr
	}

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
			e, err := convertTrackingEvent(event, h.logger)
			if err != nil {
				h.logger.LogAttrs(context.Background(), slog.LevelError,
					"impossible to convert the event to a tracking event",
					cmdlog.Err(err),
				)

				continue
			}
			flagset.CollectTrackingEventData(e)
			counterTracking++
		default:
			e, err := convertFeatureEvent(event, reqBody.Meta, h.logger)
			if err != nil {
				h.logger.LogAttrs(
					context.Background(),
					slog.LevelError,
					"impossible to convert the event to a feature event",
					cmdlog.Err(err),
				)

				continue
			}
			flagset.CollectEventData(e)
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
	event map[string]any,
	logger *slog.Logger,
) (exporter.TrackingEvent, error) {
	var e exporter.TrackingEvent
	marshalled, err := json.Marshal(event)
	if err != nil {
		return exporter.TrackingEvent{}, err
	}
	err = json.Unmarshal(marshalled, &e)
	if err != nil {
		return exporter.TrackingEvent{}, err
	}
	e.CreationDate = formatCreationDate(e.CreationDate, logger)

	return e, nil
}

func convertFeatureEvent(event map[string]any,
	metadata exporter.FeatureEventMetadata,
	logger *slog.Logger) (exporter.FeatureEvent, error) {
	var e exporter.FeatureEvent
	err := mapstructure.Decode(event, &e)
	if err != nil {
		return exporter.FeatureEvent{}, err
	}
	if e.Source == "" {
		e.Source = "PROVIDER_CACHE"
	}
	if metadata != nil {
		e.Metadata = metadata
	}
	e.CreationDate = formatCreationDate(e.CreationDate, logger)

	return e, nil
}

func formatCreationDate(input int64, logger *slog.Logger) int64 {
	if input > maxCreationDateSeconds {
		logger.LogAttrs(context.Background(), slog.LevelWarn,
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

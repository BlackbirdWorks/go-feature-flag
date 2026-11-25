package controller

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/helper"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/cmdhelpers/configfile"
	"github.com/blackbirdworks/semaphore/modules/core/flag"
)

type FlagConfigurationAPICtrl struct {
	flagsetManager service.FlagsetManager
	metrics        metric.Metrics
}

func NewAPIFlagConfiguration(flagsetManager service.FlagsetManager, metrics metric.Metrics) *FlagConfigurationAPICtrl {
	return &FlagConfigurationAPICtrl{
		flagsetManager: flagsetManager,
		metrics:        metrics,
	}
}

type FlagConfigurationRequest struct {
	Flags []string `json:"flags" required:"false"`
}

type FlagConfigurationError = string

const (
	FlagConfigErrorInvalidRequest  FlagConfigurationError = "INVALID_REQUEST"
	FlagConfigErrorRetrievingFlags FlagConfigurationError = "RETRIEVING_FLAGS_ERROR"
)

type FlagConfigurationResponse struct {
	Flags                       map[string]flag.Flag `json:"flags,omitempty"`
	EvaluationContextEnrichment map[string]any       `json:"evaluationContextEnrichment,omitempty"`
	ErrorCode                   string               `json:"errorCode,omitempty"`
	ErrorDetails                string               `json:"errorDetails,omitempty"`
}

// FlagConfigurationInput is the request of the flag configuration endpoint.
type FlagConfigurationInput struct {
	Request

	IfNoneMatch string `doc:"The request will be processed only if ETag doesn't match." header:"If-None-Match"`
	// RawBody is the JSON document of a FlagConfigurationRequest, the handler decodes it.
	RawBody []byte `contentType:"application/json"`
}

// FlagConfigurationOutput is the response of the flag configuration endpoint.
type FlagConfigurationOutput struct {
	LastModified string `header:"Last-Modified"`
	Body         FlagConfigurationResponse
	Status       int
}

// Handle is the endpoint to poll if you want to get the configuration of the flags. A request that can not be
// served is answered with the FlagConfigurationResponse that describes the error.
func (h *FlagConfigurationAPICtrl) Handle(
	ctx context.Context,
	input *FlagConfigurationInput,
) (*FlagConfigurationOutput, error) {
	h.metrics.IncFlagConfigurationCall()

	ctx, span := otel.GetTracerProvider().Tracer(configfile.OtelTracerName).Start(ctx, "flagConfiguration")
	defer span.End()

	reqBody := new(FlagConfigurationRequest)

	err := helper.BindRawBody(input.ContentType(), input.RawBody, reqBody)
	if err != nil {
		return &FlagConfigurationOutput{
			Status:       http.StatusBadRequest,
			LastModified: "",
			Body: FlagConfigurationResponse{
				Flags:                       nil,
				EvaluationContextEnrichment: nil,
				ErrorCode:                   FlagConfigErrorInvalidRequest,
				ErrorDetails:                fmt.Sprintf("impossible to read request body: %s", err),
			},
		}, nil
	}

	flagset, httpErr := helper.RequestFlagSet(ctx, h.flagsetManager, input.APIKey())
	if httpErr != nil {
		return nil, httpErr
	}

	flags, err := flagset.GetFlagsFromCache()
	if err != nil {
		return &FlagConfigurationOutput{
			Status:       http.StatusInternalServerError,
			LastModified: "",
			Body: FlagConfigurationResponse{
				Flags:                       nil,
				EvaluationContextEnrichment: nil,
				ErrorCode:                   FlagConfigErrorRetrievingFlags,
				ErrorDetails:                fmt.Sprintf("impossible to retrieve flag configuration: %s", err),
			},
		}, nil
	}

	// filter if we have a list of flags in the request.
	if len(reqBody.Flags) > 0 {
		tmpFlags := map[string]flag.Flag{}

		for _, flagKey := range reqBody.Flags {
			if _, ok := flags[flagKey]; ok {
				tmpFlags[flagKey] = flags[flagKey]
			}
		}

		flags = tmpFlags
	}

	span.SetAttributes(attribute.Int("flagConfiguration.configurationSize", len(flags)))

	return &FlagConfigurationOutput{
		Status:       http.StatusOK,
		LastModified: flagset.GetCacheRefreshDate().Format(time.RFC1123),
		Body: FlagConfigurationResponse{
			EvaluationContextEnrichment: flagset.GetEvaluationContextEnrichment(),
			Flags:                       flags,
			ErrorCode:                   "",
			ErrorDetails:                "",
		},
	}, nil
}

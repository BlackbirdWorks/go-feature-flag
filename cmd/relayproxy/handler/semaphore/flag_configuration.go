package semaphore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/helper"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/cmdhelpers/configfile"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
)

// FlagConfigurationAPICtrl serves the flag configuration API of the relay proxy.
type FlagConfigurationAPICtrl struct {
	flagsetManager service.FlagSetResolver
	metrics        *metric.Metrics
}

// NewAPIFlagConfiguration returns the controller that serves the flag configuration API.
func NewAPIFlagConfiguration(
	flagsetManager service.FlagSetResolver, metrics *metric.Metrics,
) *FlagConfigurationAPICtrl {
	return &FlagConfigurationAPICtrl{
		flagsetManager: flagsetManager,
		metrics:        metrics,
	}
}

const emptyObject = "{}"

var errNotAnObject = errors.New("the flag is not encoded as a JSON object")

// FlagConfigurationRequest is the request body of the flag configuration API.
type FlagConfigurationRequest struct {
	Flags []string `json:"flags" required:"false"`
}

// FlagConfigurationError is the error code returned by the flag configuration API.
type FlagConfigurationError = string

const (
	// FlagConfigErrorInvalidRequest is the error code of a request that cannot be parsed.
	FlagConfigErrorInvalidRequest FlagConfigurationError = "INVALID_REQUEST"
	// FlagConfigErrorRetrievingFlags is the error code returned when the flags cannot be retrieved.
	FlagConfigErrorRetrievingFlags FlagConfigurationError = "RETRIEVING_FLAGS_ERROR"
)

// FlagConfigurationResponse is the response body of the flag configuration API.
type FlagConfigurationResponse struct {
	Flags                       map[string]flagdef.Flag `json:"flags,omitempty"`
	EvaluationContextEnrichment map[string]any          `json:"evaluationContextEnrichment,omitempty"`
	ErrorCode                   string                  `json:"errorCode,omitempty"`
	ErrorDetails                string                  `json:"errorDetails,omitempty"`
}

// flagWithSegments is a flag of the configuration that uses segments: the flag as it is encoded, with the names of the
// segments its rules use and whether one is dynamic. No member and no key pattern is ever listed.
type flagWithSegments struct {
	flagdef.Flag

	segments       []string
	requiresServer bool
}

// MarshalJSON encodes the flag with its two fields added at the end.
func (f flagWithSegments) MarshalJSON() ([]byte, error) {
	data, err := json.Marshal(f.Flag)
	if err != nil {
		return nil, fmt.Errorf("encoding the flag: %w", err)
	}

	extra, err := json.Marshal(struct {
		Segments                 []string `json:"segments"`
		RequiresServerEvaluation bool     `json:"requiresServerEvaluation"`
	}{Segments: f.segments, RequiresServerEvaluation: f.requiresServer})
	if err != nil {
		return nil, fmt.Errorf("encoding the segments of the flag: %w", err)
	}

	if len(data) < len(emptyObject) || data[0] != '{' || data[len(data)-1] != '}' {
		return nil, fmt.Errorf("%w: %s", errNotAnObject, data)
	}

	out := make([]byte, 0, len(data)+len(extra))
	out = append(out, data[:len(data)-1]...)

	if len(data) > len(emptyObject) {
		out = append(out, ',')
	}

	return append(out, extra[1:]...), nil
}

// withSegments marks the flags that use segments. The map it is given is not changed: a map that holds a marked
// flag is a copy.
func withSegments(flags map[string]flagdef.Flag, user service.SegmentUser) map[string]flagdef.Flag {
	var out map[string]flagdef.Flag

	for key, flag := range flags {
		names, requires := user.FlagSegments(key)
		if len(names) == 0 {
			continue
		}

		if out == nil {
			out = maps.Clone(flags)
		}

		out[key] = flagWithSegments{Flag: flag, segments: names, requiresServer: requires}
	}

	if out == nil {
		return flags
	}

	return out
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
		return nil, fmt.Errorf("getting the flag set of the flag configuration: %w", httpErr)
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
		tmpFlags := map[string]flagdef.Flag{}

		for _, flagKey := range reqBody.Flags {
			if _, ok := flags[flagKey]; ok {
				tmpFlags[flagKey] = flags[flagKey]
			}
		}

		flags = tmpFlags
	}

	if user, ok := flagset.(service.SegmentUser); ok {
		flags = withSegments(flags, user)
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

package ofrep

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/helper"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/model"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/cmdhelpers/configfile"
	"github.com/blackbirdworks/semaphore/internal/flagstate"
	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/flag"
	"github.com/blackbirdworks/semaphore/modules/core/utils"
)

type EvaluateCtrl struct {
	flagsetManager service.FlagsetManager
	metrics        metric.Metrics
	eventStream    config.OfrepEventStream
	// legacyMetadataOnly drops MetadataCacheable, LegacyMetadataCacheable is always sent.
	legacyMetadataOnly bool
}

// Option customizes an EvaluateCtrl.
type Option func(*EvaluateCtrl)

// WithLegacyMetadataOnly stops the controller from adding the Semaphore metadata keys
// (semaphore_cacheable) next to the legacy ones.
func WithLegacyMetadataOnly(legacyOnly bool) Option {
	return func(h *EvaluateCtrl) { h.legacyMetadataOnly = legacyOnly }
}

func NewOFREPEvaluate(
	flagsetManager service.FlagsetManager,
	metrics metric.Metrics,
	eventStream config.OfrepEventStream,
	opts ...Option,
) EvaluateCtrl {
	ctrl := EvaluateCtrl{
		flagsetManager: flagsetManager,
		metrics:        metrics,
		eventStream:    eventStream,
	}
	for _, opt := range opts {
		opt(&ctrl)
	}

	return ctrl
}

// Evaluate is the entry point to evaluate a flag using the OpenFeature Remote Evaluation Protocol.
func (h *EvaluateCtrl) Evaluate(w http.ResponseWriter, r *http.Request) error {
	flagKey := r.PathValue("flagKey")
	if flagKey == "" {
		return helper.WriteJSON(w, http.StatusBadRequest, NewEvaluateError(flagKey, flag.ErrorCodeGeneral,
			"No key provided in the URL"))
	}

	reqBody := new(model.OFREPEvalFlagRequest)

	err := helper.BindBody(r, reqBody)
	if err != nil {
		return helper.WriteJSON(
			w,
			http.StatusBadRequest,
			NewEvaluateError(flagKey, flag.ErrorCodeInvalidContext, err.Error()),
		)
	}

	validateErr := assertOFREPEvaluateRequest(reqBody)
	if validateErr != nil {
		return helper.WriteJSON(w, http.StatusBadRequest, model.OFREPEvaluateResponseError{
			OFREPCommonResponseError: *validateErr,
			Key:                      flagKey,
		})
	}

	evalCtx, err := evaluationContextFromOFREPRequest(reqBody)
	if err != nil {
		return helper.WriteJSON(w, http.StatusBadRequest, err)
	}

	tracer := otel.GetTracerProvider().Tracer(configfile.OtelTracerName)

	_, span := tracer.Start(r.Context(), "flagEvaluation")
	defer span.End()

	flagset, httpErr := helper.RequestFlagSet(r.Context(), h.flagsetManager, helper.APIKey(r))
	if httpErr != nil {
		return httpErr
	}

	// we set a nil value to the default value to avoid the default value to be used.
	var defaultValue any

	flagValue, _ := flagset.RawVariation(flagKey, evalCtx, defaultValue)
	if flagValue.ErrorCode == flag.ErrorCodeFlagNotFound {
		h.metrics.IncFlagNotFoundEvaluation()
	} else {
		h.metrics.IncFlagEvaluation(flagKey)
	}

	if flagValue.Reason == flag.ReasonError {
		httpStatus := http.StatusBadRequest
		if flagValue.ErrorCode == flag.ErrorCodeFlagNotFound {
			httpStatus = http.StatusNotFound
		}

		return helper.WriteJSON(w, httpStatus, NewEvaluateError(flagKey, flagValue.ErrorCode,
			fmt.Sprintf("Error while evaluating the flag: %s", flagKey)))
	}

	span.SetAttributes(
		attribute.String("flagEvaluation.flagName", flagKey),
		attribute.Bool("flagEvaluation.trackEvents", flagValue.TrackEvents),
		attribute.String("flagEvaluation.variant", flagValue.VariationType),
		attribute.Bool("flagEvaluation.failed", flagValue.Failed),
		attribute.String("flagEvaluation.version", flagValue.Version),
		attribute.String("flagEvaluation.reason", flagValue.Reason),
		attribute.String("flagEvaluation.errorCode", flagValue.ErrorCode),
		attribute.Bool("flagEvaluation.cacheable", flagValue.Cacheable),
		// we convert to string because there is no attribute for interface{}
		attribute.String("flagEvaluation.value", fmt.Sprintf("%v", flagValue.Value)),
	)

	metadata := flagValue.Metadata
	if flagValue.Cacheable {
		if metadata == nil {
			metadata = make(map[string]any)
		}

		if !h.legacyMetadataOnly {
			metadata[MetadataCacheable] = true
		}

		metadata[LegacyMetadataCacheable] = true
	}

	return helper.WriteJSON(w, http.StatusOK, model.OFREPEvaluateSuccessResponse{
		Key:      flagKey,
		Value:    flagValue.Value,
		Reason:   flagValue.Reason,
		Variant:  flagValue.VariationType,
		Metadata: metadata,
	})
}

// BulkEvaluate is the entry point to evaluate in bulk flags using the OpenFeature Remote Evaluation Protocol.
func (h *EvaluateCtrl) BulkEvaluate(w http.ResponseWriter, r *http.Request) error {
	request := new(model.OFREPEvalFlagRequest)

	err := helper.BindBody(r, request)
	if err != nil {
		return helper.WriteJSON(w, http.StatusBadRequest, NewOFREPCommonError("INVALID_CONTEXT", err.Error()))
	}

	validateErr := assertOFREPEvaluateRequest(request)
	if validateErr != nil {
		return helper.WriteJSON(w, http.StatusBadRequest, validateErr)
	}

	evalCtx, err := evaluationContextFromOFREPRequest(request)
	if err != nil {
		return helper.WriteJSON(w, http.StatusBadRequest, err)
	}

	// if no flag is not provided, we evaluate all available flags
	response := model.OFREPBulkEvaluateSuccessResponse{
		Flags:        make([]model.OFREPFlagBulkEvaluateSuccessResponse, 0),
		EventStreams: nil,
	}
	tracer := otel.GetTracerProvider().Tracer(configfile.OtelTracerName)

	_, span := tracer.Start(r.Context(), "AllFlagsState")
	defer span.End()

	flagset, httpErr := helper.RequestFlagSet(r.Context(), h.flagsetManager, helper.APIKey(r))
	if httpErr != nil {
		return httpErr
	}

	var allFlagsResp flagstate.AllFlags
	if len(evalCtx.ExtractGOFFProtectedFields().FlagList) > 0 {
		// if we have a list of flags to evaluate in the evaluation context, we evaluate only those flags.
		allFlagsResp = flagset.GetFlagStates(evalCtx, evalCtx.ExtractGOFFProtectedFields().FlagList)
	} else {
		allFlagsResp = flagset.AllFlagsState(evalCtx)
	}

	flagNames := make([]string, 0, len(allFlagsResp.GetFlags()))
	for key, val := range allFlagsResp.GetFlags() {
		flagNames = append(flagNames, key)

		value := val.Value
		if val.Reason == flag.ReasonError {
			value = nil
		}

		response.Flags = append(response.Flags, model.OFREPFlagBulkEvaluateSuccessResponse{
			Key:          key,
			Value:        value,
			Reason:       val.Reason,
			Variant:      val.VariationType,
			Metadata:     val.Metadata,
			ErrorCode:    val.ErrorCode,
			ErrorDetails: val.ErrorDetails,
		})
	}

	h.metrics.IncAllFlag(flagNames...)

	sort.Slice(response.Flags, func(i, j int) bool {
		return response.Flags[i].Key < response.Flags[j].Key
	})

	span.SetAttributes(
		attribute.Int("AllFlagsState.numberEvaluation", len(response.Flags)),
	)

	if h.eventStream.IsEnabled() {
		response.EventStreams = h.buildEventStreams(r)
	}

	return helper.WriteJSON(w, http.StatusOK, response)
}

// ofrepSSEPath is the path of the relay proxy endpoint streaming flag changes over SSE.
const (
	// MetadataCacheable is the response metadata key telling a provider that it can cache the value.
	MetadataCacheable = "semaphore_cacheable"
	// LegacyMetadataCacheable is the key GO Feature Flag used for the same information. The upstream
	// providers read it, so it is still sent next to MetadataCacheable.
	LegacyMetadataCacheable = "gofeatureflag_cacheable"
)

const ofrepSSEPath = "/stream/v1/sse/flag/change"

// buildEventStreams returns the eventStreams advertised in the OFREP bulk evaluation
// response (OpenFeature ADR-0008).
// The URL is built from the configured base URL
// and contains the API key of the caller, so the provider can connect without extra credentials.
// This URL is sensitive, it must never be logged.
func (h *EvaluateCtrl) buildEventStreams(r *http.Request) []model.OFREPEventStream {
	baseURL := h.eventStream.BaseURL

	parsedURL, err := url.Parse(baseURL)
	if err != nil {
		return nil
	}

	parsedURL = parsedURL.JoinPath(ofrepSSEPath)
	if apiKey := helper.APIKey(r); apiKey != "" {
		parsedURL.RawQuery = url.Values{"apiKey": []string{apiKey}}.Encode()
	}

	return []model.OFREPEventStream{{
		Type:               "sse",
		URL:                parsedURL.String(),
		InactivityDelaySec: h.eventStream.InactivityDelaySec,
	}}
}

func assertOFREPEvaluateRequest(
	ofrepEvalReq *model.OFREPEvalFlagRequest,
) *model.OFREPCommonResponseError {
	if ofrepEvalReq.Context == nil {
		return NewOFREPCommonError(flag.ErrorCodeInvalidContext,
			"Semaphore requires an evaluation context in the request.")
	}

	// An empty context object is allowed since the evaluation context is optional.
	// If the context does not have any targetingKey, this is fine since the core
	// evaluation logic will handle if it is required or not.

	return nil
}

func evaluationContextFromOFREPRequest(req *model.OFREPEvalFlagRequest) (ffcontext.EvaluationContext, error) {
	if req == nil || req.Context == nil {
		return ffcontext.EvaluationContext{}, NewOFREPCommonError(
			flag.ErrorCodeInvalidContext,
			"Semaphore has received an invalid context.")
	}

	ctx := req.Context

	// targetingKey is optional, it is only required if the flag needs bucketing and
	// the check is done in the core evaluation logic.
	// If we don't have a targetingKey, we return an empty string.
	targetingKey := ""
	if key, ok := ctx["targetingKey"].(string); ok {
		targetingKey = key
	}

	// Create evaluation context (empty targeting key is allowed)
	evalCtx := utils.ConvertEvaluationCtxFromRequest(targetingKey, ctx)

	return evalCtx, nil
}

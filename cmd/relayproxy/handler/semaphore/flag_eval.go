package semaphore

import (
	"errors"
	"fmt"
	"net/http"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/helper"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/model"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/cmdhelpers/configfile"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
)

var (
	errImpossibleToFindThe = errors.New("impossible to find the flag key in the URL")
)

// FlagEval is the controller for the /v1/feature/{flagKey}/eval endpoint.
type FlagEval struct {
	flagsetMngr service.FlagSetResolver
	metrics     *metric.Metrics
}

// NewFlagEval initializes the controller for the /v1/feature/{flagKey}/eval endpoint.
func NewFlagEval(flagsetMngr service.FlagSetResolver, metrics *metric.Metrics) *FlagEval {
	return &FlagEval{
		flagsetMngr: flagsetMngr,
		metrics:     metrics,
	}
}

// Handler is the entry point for the flag eval endpoint.
func (h *FlagEval) Handler(w http.ResponseWriter, r *http.Request) error {
	flagKey := r.PathValue("flagKey")
	if flagKey == "" {
		return errImpossibleToFindThe
	}

	reqBody := new(model.EvalFlagRequest)

	err := helper.BindBody(r, reqBody)
	if err != nil {
		return fmt.Errorf("reading the request of the flag evaluation: %w", err)
	}

	// validation that we have a reqBody key
	assertErr := assertRequest(&reqBody.AllFlagRequest)
	if assertErr != nil {
		return assertErr
	}

	evaluationCtx, err := evaluationContextFromRequest(&reqBody.AllFlagRequest)
	if err != nil {
		return err
	}

	tracer := otel.GetTracerProvider().Tracer(configfile.OtelTracerName)

	_, span := tracer.Start(r.Context(), "flagEvaluation")
	defer span.End()

	flagset, httpErr := helper.RequestFlagSet(r.Context(), h.flagsetMngr, helper.APIKey(r))
	if httpErr != nil {
		return fmt.Errorf("getting the flag set of the flag evaluation: %w", httpErr)
	}

	flagValue, _ := flagset.RawVariation(flagKey, evaluationCtx, reqBody.DefaultValue)
	if flagValue.ErrorCode == flagdef.ErrorCodeFlagNotFound {
		h.metrics.IncFlagNotFoundEvaluation()
	} else {
		h.metrics.IncFlagEvaluation(flagKey)
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

	flagsetName, flagSetNameErr := h.flagsetMngr.FlagSetName(helper.APIKey(r))
	if flagSetNameErr == nil {
		span.SetAttributes(attribute.String("flagEvaluation.flagSetName", flagsetName))
	}

	werr := helper.WriteJSON(w, http.StatusOK, flagValue)
	if werr != nil {
		return fmt.Errorf("writing the response of the flag evaluation: %w", werr)
	}

	return nil
}

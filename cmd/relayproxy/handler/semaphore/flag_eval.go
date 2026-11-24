package controller

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
	"github.com/blackbirdworks/semaphore/modules/core/flag"
)

var (
	errImpossibleToFindThe = errors.New("impossible to find the flag key in the URL")
)

type flagEval struct {
	flagsetMngr service.FlagsetManager
	metrics     metric.Metrics
}

func NewFlagEval(flagsetMngr service.FlagsetManager, metrics metric.Metrics) RawHandler {
	return &flagEval{
		flagsetMngr: flagsetMngr,
		metrics:     metrics,
	}
}

// Handler is the entry point for the flag eval endpoint.
func (h *flagEval) Handler(w http.ResponseWriter, r *http.Request) error {
	flagKey := r.PathValue("flagKey")
	if flagKey == "" {
		return errImpossibleToFindThe
	}

	reqBody := new(model.EvalFlagRequest)
	if err := helper.BindBody(r, reqBody); err != nil {
		return err
	}

	// validation that we have a reqBody key
	if err := assertRequest(&reqBody.AllFlagRequest); err != nil {
		return err
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
		return httpErr
	}

	flagValue, _ := flagset.RawVariation(flagKey, evaluationCtx, reqBody.DefaultValue)
	if flagValue.ErrorCode == flag.ErrorCodeFlagNotFound {
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

	if flagsetName, flagSetNameErr := h.flagsetMngr.FlagSetName(helper.APIKey(r)); flagSetNameErr == nil {
		span.SetAttributes(attribute.String("flagEvaluation.flagSetName", flagsetName))
	}

	return helper.WriteJSON(w, http.StatusOK, flagValue)
}

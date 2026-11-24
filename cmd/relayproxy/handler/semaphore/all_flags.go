package controller

import (
	"net/http"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/helper"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/model"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/cmdhelpers/configfile"
	"github.com/blackbirdworks/semaphore/internal/flagstate"
)

type allFlags struct {
	flagsetManager service.FlagsetManager
	metrics        metric.Metrics
}

func NewAllFlags(flagsetManager service.FlagsetManager, metrics metric.Metrics) RawHandler {
	return &allFlags{
		flagsetManager: flagsetManager,
		metrics:        metrics,
	}
}

// Handler is the entry point for the allFlags endpoint.
func (h *allFlags) Handler(w http.ResponseWriter, r *http.Request) error {
	reqBody := new(model.AllFlagRequest)
	if err := helper.BindBody(r, reqBody); err != nil {
		return err
	}
	// validation that we have a reqBody key
	if err := assertRequest(reqBody); err != nil {
		return err
	}

	evaluationCtx, err := evaluationContextFromRequest(reqBody)
	if err != nil {
		return err
	}
	tracer := otel.GetTracerProvider().Tracer(configfile.OtelTracerName)
	_, span := tracer.Start(r.Context(), "AllFlagsState")
	defer span.End()

	flagset, httpErr := helper.RequestFlagSet(r.Context(), h.flagsetManager, helper.APIKey(r))
	if httpErr != nil {
		return httpErr
	}

	var allFlags flagstate.AllFlags
	if len(evaluationCtx.ExtractGOFFProtectedFields().FlagList) > 0 {
		// if we have a list of flags to evaluate in the evaluation context, we evaluate only those flags.
		allFlags = flagset.GetFlagStates(
			evaluationCtx,
			evaluationCtx.ExtractGOFFProtectedFields().FlagList,
		)
	} else {
		allFlags = flagset.AllFlagsState(evaluationCtx)
	}

	flagNames := make([]string, 0, len(allFlags.GetFlags()))
	for key := range allFlags.GetFlags() {
		flagNames = append(flagNames, key)
	}
	h.metrics.IncAllFlag(flagNames...)

	span.SetAttributes(
		attribute.Bool("AllFlagsState.valid", allFlags.IsValid()),
		attribute.Int("AllFlagsState.numberEvaluation", len(allFlags.GetFlags())),
	)

	return helper.WriteJSON(w, http.StatusOK, allFlags)
}

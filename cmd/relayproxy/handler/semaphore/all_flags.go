package controller

import (
	"net/http"

	"github.com/labstack/echo/v5"
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

func NewAllFlags(flagsetManager service.FlagsetManager, metrics metric.Metrics) Controller {
	return &allFlags{
		flagsetManager: flagsetManager,
		metrics:        metrics,
	}
}

// Handler is the entry point for the allFlags endpoint
// @Summary      All flags variations for a user
// @Tags Semaphore Evaluation API
// @Description  Making a **POST** request to the URL `/v1/allflags` will give you the values of all the flags for
// @Description this user.
// @Description
// @Description To get a variation you should provide information about the user.
// @Description For that you should provide some user information in JSON in the request body.
// @Security     ApiKeyAuth
// @Security     XApiKeyAuth
// @Produce      json
// @Accept		 json
// @Param 	     data body model.AllFlagRequest true "Evaluation context to challenge against the flags."
// @Success      200  {object} modeldocs.AllFlags "Success"
// @Failure      400 {object} modeldocs.HTTPErrorDoc "Bad Request"
// @Failure      500 {object} modeldocs.HTTPErrorDoc "Internal server error"
// @Router       /v1/allflags [post]
func (h *allFlags) Handler(c *echo.Context) error {
	reqBody := new(model.AllFlagRequest)
	if err := c.Bind(reqBody); err != nil {
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
	_, span := tracer.Start(c.Request().Context(), "AllFlagsState")
	defer span.End()

	flagset, httpErr := helper.FlagSet(h.flagsetManager, helper.APIKey(c))
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
	return c.JSON(http.StatusOK, allFlags)
}

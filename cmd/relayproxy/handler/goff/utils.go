package controller

import (
	"net/http"

	"github.com/labstack/echo/v5"
	"github.com/thomaspoignant/go-feature-flag/cmd/relayproxy/model"
	"github.com/thomaspoignant/go-feature-flag/ffcontext"
	"github.com/thomaspoignant/go-feature-flag/modules/core/utils"
)

// errLegacyUser is the message returned when a request still uses the removed `user` field.
const errLegacyUser = "assertRequest: the 'user' field is no longer supported, " +
	"send an 'evaluationContext' instead"

// assertRequest is the function which validates the request, if not valid an echo.HTTPError is return.
func assertRequest(u *model.AllFlagRequest) *echo.HTTPError {
	if u != nil && u.LegacyUser != nil {
		return echo.NewHTTPError(http.StatusBadRequest, errLegacyUser)
	}
	if u == nil || u.EvaluationContext == nil {
		return echo.NewHTTPError(
			http.StatusBadRequest,
			"assertRequest: impossible to find evaluationContext in request",
		)
	}
	return nil
}

func evaluationContextFromRequest(req *model.AllFlagRequest) (ffcontext.Context, error) {
	if req == nil || req.EvaluationContext == nil {
		return ffcontext.EvaluationContext{},
			echo.NewHTTPError(
				http.StatusBadRequest,
				"evaluationContextFromRequest: impossible to convert the request, evaluationContext missing",
			)
	}
	u := req.EvaluationContext
	return utils.ConvertEvaluationCtxFromRequest(u.Key, u.Custom), nil
}

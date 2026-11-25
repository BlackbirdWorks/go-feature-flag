package controller

import (
	"net/http"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/helper"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/model"
	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/utils"
)

// errLegacyUser is the message returned when a request still uses the removed `user` field.
const errLegacyUser = "assertRequest: the 'user' field is no longer supported, " +
	"send an 'evaluationContext' instead"

// assertRequest is the function which validates the request, if not valid a helper.HTTPError is return.
func assertRequest(u *model.AllFlagRequest) *helper.HTTPError {
	if u != nil && u.LegacyUser != nil {
		return helper.NewHTTPError(http.StatusBadRequest, errLegacyUser)
	}
	if u == nil || u.EvaluationContext == nil {
		return helper.NewHTTPError(
			http.StatusBadRequest,
			"assertRequest: impossible to find evaluationContext in request",
		)
	}

	return nil
}

func evaluationContextFromRequest(req *model.AllFlagRequest) (ffcontext.EvaluationContext, error) {
	if req == nil || req.EvaluationContext == nil {
		return ffcontext.EvaluationContext{},
			helper.NewHTTPError(
				http.StatusBadRequest,
				"evaluationContextFromRequest: impossible to convert the request, evaluationContext missing",
			)
	}
	u := req.EvaluationContext

	return utils.ConvertEvaluationCtxFromRequest(u.Key, u.Custom), nil
}

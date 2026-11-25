package ofrep

import (
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/model"
	"github.com/blackbirdworks/semaphore/modules/core/flag"
)

// NewEvaluateError returns the OFREP error response for a failed flag evaluation.
func NewEvaluateError(
	key string,
	errorCode flag.ErrorCode,
	errorDetails string,
) *model.OFREPEvaluateResponseError {
	return &model.OFREPEvaluateResponseError{
		ErrorCode:    errorCode,
		ErrorDetails: errorDetails,
		Key:          key,
	}
}

// NewOFREPCommonError returns the OFREP error response shared by the OFREP endpoints.
func NewOFREPCommonError(
	errorCode flag.ErrorCode,
	errorDetails string,
) *model.OFREPCommonResponseError {
	return &model.OFREPCommonResponseError{
		ErrorCode:    errorCode,
		ErrorDetails: errorDetails,
	}
}

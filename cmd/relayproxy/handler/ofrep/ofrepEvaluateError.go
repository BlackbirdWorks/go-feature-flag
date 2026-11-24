package ofrep

import (
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/model"
	"github.com/blackbirdworks/semaphore/modules/core/flag"
)

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

func NewOFREPCommonError(
	errorCode flag.ErrorCode,
	errorDetails string,
) *model.OFREPCommonResponseError {
	return &model.OFREPCommonResponseError{
		ErrorCode:    errorCode,
		ErrorDetails: errorDetails,
	}
}

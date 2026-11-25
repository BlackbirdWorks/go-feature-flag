package model

import (
	"fmt"

	"github.com/blackbirdworks/semaphore/modules/core/flag"
)

// OFREPEvaluateResponseError is the error response of an OFREP flag evaluation.
type OFREPEvaluateResponseError struct {
	OFREPCommonResponseError `yaml:",inline"`

	Key string `json:"key"`
}

// OFREPCommonResponseError is the error response shared by the OFREP endpoints.
type OFREPCommonResponseError struct {
	ErrorCode    flag.ErrorCode `enum:"PROVIDER_NOT_READY,FLAG_NOT_FOUND,PARSE_ERROR,TYPE_MISMATCH,GENERAL,INVALID_CONTEXT,TARGETING_KEY_MISSING,FLAG_CONFIG" json:"errorCode"` //nolint:lll // struct tags can not be wrapped
	ErrorDetails string         `json:"errorDetails"`
}

func (o *OFREPCommonResponseError) Error() string {
	return fmt.Sprintf("[%s] %s", o.ErrorCode, o.ErrorDetails)
}

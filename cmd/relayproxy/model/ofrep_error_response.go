package model

import (
	"fmt"

	"github.com/blackbirdworks/semaphore/modules/core/flag"
)

type OFREPEvaluateResponseError struct {
	OFREPCommonResponseError `json:",inline" yaml:",inline" toml:",inline"`

	Key string `json:"key"`
}

type OFREPCommonResponseError struct {
	ErrorCode    flag.ErrorCode `json:"errorCode"    enum:"PROVIDER_NOT_READY,FLAG_NOT_FOUND,PARSE_ERROR,TYPE_MISMATCH,GENERAL,INVALID_CONTEXT,TARGETING_KEY_MISSING,FLAG_CONFIG"` //nolint:lll // struct tags can not be wrapped
	ErrorDetails string         `json:"errorDetails"`
}

func (o *OFREPCommonResponseError) Error() string {
	return fmt.Sprintf("[%s] %s", o.ErrorCode, o.ErrorDetails)
}

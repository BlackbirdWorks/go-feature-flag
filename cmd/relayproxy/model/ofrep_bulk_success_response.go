package model

// OFREPBulkEvaluateSuccessResponse is the response body of a successful OFREP bulk evaluation.
type OFREPBulkEvaluateSuccessResponse struct {
	Flags        []OFREPFlagBulkEvaluateSuccessResponse `json:"flags"`
	EventStreams []OFREPEventStream                     `json:"eventStreams,omitempty"`
}

// OFREPEventStream describes a stream a provider can subscribe to in order to be notified
// of flag changes instead of polling, as defined by OpenFeature ADR-0008.
type OFREPEventStream struct {
	Type               string `json:"type"`
	URL                string `json:"url"`
	InactivityDelaySec int    `json:"inactivityDelaySec,omitempty"`
}

// OFREPFlagBulkEvaluateSuccessResponse is the result of one flag in an OFREP bulk evaluation.
type OFREPFlagBulkEvaluateSuccessResponse struct {
	OFREPEvaluateSuccessResponse

	ErrorCode    string `json:"errorCode,omitempty"`
	ErrorDetails string `json:"errorDetails,omitempty"`
}

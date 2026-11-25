package model

// OFREPEvalFlagRequest is the request body of an OFREP flag evaluation.
//
//nolint:lll // struct tags can not be wrapped
type OFREPEvalFlagRequest struct {
	Context map[string]any `example:"{\"targetingKey\":\"4f433951-4c8c-42b3-9f18-8c9a5ed8e9eb\",\"firstname\":\"John\",\"lastname\":\"Doe\",\"company\":\"Semaphore\"}" form:"context" json:"context" query:"context" required:"false" xml:"context"`
}

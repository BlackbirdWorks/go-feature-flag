package model

//nolint:lll // struct tags can not be wrapped
type OFREPEvalFlagRequest struct {
	Context map[string]any `json:"context" required:"false" xml:"context" form:"context" query:"context" example:"{\"targetingKey\":\"4f433951-4c8c-42b3-9f18-8c9a5ed8e9eb\",\"firstname\":\"John\",\"lastname\":\"Doe\",\"company\":\"Semaphore\"}"`
}

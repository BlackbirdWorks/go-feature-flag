package model

//nolint:lll // swag annotations are parsed line by line
type OFREPEvalFlagRequest struct {
	Context map[string]any `json:"context" xml:"context" form:"context" query:"context" swaggertype:"object,string" example:"targetingKey:4f433951-4c8c-42b3-9f18-8c9a5ed8e9eb,firstname:John,lastname:Doe,company:Semaphore"`
}

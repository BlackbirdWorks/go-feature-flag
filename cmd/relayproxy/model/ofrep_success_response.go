package model

type OFREPEvaluateSuccessResponse struct {
	Value    any            `json:"value"`
	Metadata map[string]any `json:"metadata,omitempty"`
	Key      string         `json:"key"`
	Reason   string         `json:"reason"`
	Variant  string         `json:"variant"`
}

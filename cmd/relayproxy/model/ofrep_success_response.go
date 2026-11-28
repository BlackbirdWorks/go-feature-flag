package model

// OFREPEvaluateSuccessResponse is the OFREP evaluation result (also the bulk result items, whose strong ETag hashes
// the serialized bytes).
//
//nolint:govet // fieldalignment: field order is the serialized key order, which the bulk ETag depends on
type OFREPEvaluateSuccessResponse struct {
	Key      string         `json:"key"`
	Value    any            `json:"value,omitempty"`
	Reason   string         `json:"reason"`
	Variant  string         `json:"variant"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

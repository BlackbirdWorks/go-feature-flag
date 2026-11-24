package model

import (
	"encoding/json"

	"github.com/blackbirdworks/semaphore/modules/core/flag"
)

// JSONType contains all acceptable flag value types.
type JSONType interface {
	float64 | int | string | bool | any | map[string]any
}

// VariationResult contains all the field available in a flag variation result.
//
//nolint:govet // fieldalignment: field order is the key order of the CLI and API output
type VariationResult[T JSONType] struct {
	TrackEvents   bool                  `json:"trackEvents"`
	VariationType string                `json:"variationType"`
	Failed        bool                  `json:"failed"`
	Version       string                `json:"version"`
	Reason        flag.ResolutionReason `json:"reason"`
	ErrorCode     flag.ErrorCode        `json:"errorCode"`
	ErrorDetails  string                `json:"errorDetails,omitempty"`
	Value         T                     `json:"value"`
	Cacheable     bool                  `json:"cacheable"`
	Metadata      map[string]any        `json:"metadata,omitempty"`
}

// ToJSONStr converts the VariationResult to a JSON string.
func (v VariationResult[T]) ToJSONStr() string {
	content, _ := json.Marshal(v)

	return string(content)
}

// RawVarResult is the result of the raw variation call.
// This is used by ffclient.RawVariation functions, this should be used only by internal calls.
type RawVarResult struct { //nolint:govet // fieldalignment: field order is the key order of the CLI and API output
	TrackEvents   bool                  `json:"trackEvents"`
	VariationType string                `json:"variationType"`
	Failed        bool                  `json:"failed"`
	Version       string                `json:"version"`
	Reason        flag.ResolutionReason `json:"reason"`
	ErrorCode     flag.ErrorCode        `json:"errorCode"`
	ErrorDetails  string                `json:"errorDetails,omitempty"`
	Value         any                   `json:"value"`
	Cacheable     bool                  `json:"cacheable"`
	Metadata      map[string]any        `json:"metadata,omitempty"`
}

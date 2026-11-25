package model

import "github.com/blackbirdworks/semaphore/cmdhelpers/manifest/model"

// ManifestSuccessResponse is the machine-readable manifest envelope for CLI and tooling.
type ManifestSuccessResponse struct {
	Flags []ManifestDefinitionSuccessResponse `json:"flags"`
}

// ManifestDefinitionSuccessResponse is the response body of a successful flag manifest request.
type ManifestDefinitionSuccessResponse struct {
	Key          string         `json:"key"`
	FlagType     model.FlagType `enum:"boolean,string,integer,float,object" json:"type"`
	DefaultValue any            `json:"defaultValue"`
	Description  string         `json:"description"`
}

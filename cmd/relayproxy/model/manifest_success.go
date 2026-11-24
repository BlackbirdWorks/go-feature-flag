package model

import "github.com/blackbirdworks/semaphore/cmdhelpers/manifest/model"

// ManifestSuccessResponse is the machine-readable manifest envelope for CLI and tooling.
type ManifestSuccessResponse struct {
	Flags []ManifestDefinitionSuccessResponse `json:"flags"`
}

type ManifestDefinitionSuccessResponse struct {
	Key          string         `json:"key"`
	FlagType     model.FlagType `json:"type"         enum:"boolean,string,integer,float,object"`
	DefaultValue any            `json:"defaultValue"`
	Description  string         `json:"description"`
}

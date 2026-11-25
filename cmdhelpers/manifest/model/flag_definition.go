package model

// FlagDefinition describes a flag in the flag manifest.
type FlagDefinition struct {
	FlagType     FlagType `json:"flagType"`
	DefaultValue any      `json:"defaultValue"`
	Description  string   `json:"description"`
}

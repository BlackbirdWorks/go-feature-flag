package model

// FlagType is the type of a flag in the flag manifest.
type FlagType string

const (
	// FlagTypeBoolean is the flag type of boolean flags.
	FlagTypeBoolean FlagType = "boolean"
	// FlagTypeString is the flag type of string flags.
	FlagTypeString FlagType = "string"
	// FlagTypeInteger is the flag type of integer flags.
	FlagTypeInteger FlagType = "integer"
	// FlagTypeFloat is the flag type of floating-point flags.
	FlagTypeFloat FlagType = "float"
	// FlagTypeObject is the flag type of object flags.
	FlagTypeObject FlagType = "object"
)

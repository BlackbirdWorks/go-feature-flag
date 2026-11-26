package controlplane

// FlagSetDocument is the document of kind FlagSet.
type FlagSetDocument struct {
	// APIVersion is the document version, "semaphore/v1". Empty means the current version when marshalling.
	APIVersion string `json:"apiVersion" yaml:"apiVersion"`
	// Kind is "FlagSet". Empty means "FlagSet" when marshalling.
	Kind string `json:"kind" yaml:"kind"`
	// FlagSets are the flag-set definitions.
	FlagSets []FlagSet `json:"flagSets" yaml:"flagSets"`
}

// FlagSet binds flag objects and API keys.
type FlagSet struct {
	// Name is the unique name of the flag set.
	Name string `json:"name" yaml:"name"`
	// Sources are names of manifest objects of kind flags; a later source wins on a duplicate flag key.
	Sources []string `json:"sources" yaml:"sources"`
	// APIKeys are plain inline keys (UUIDs). They are secrets: never log them.
	APIKeys []string `json:"apiKeys,omitempty" yaml:"apiKeys,omitempty"`
}

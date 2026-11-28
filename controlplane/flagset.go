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
	// SegmentSources are names of manifest objects of kind segments; the segments that the rules of this flag set
	// name with segment("name") are defined there. A name must be defined once in all the sources of a flag set.
	SegmentSources []string `json:"segmentSources,omitempty" yaml:"segmentSources,omitempty"`
	// APIKeys are plain inline keys (UUIDs). They are secrets: never log them.
	APIKeys []string `json:"apiKeys,omitempty" yaml:"apiKeys,omitempty"`
}

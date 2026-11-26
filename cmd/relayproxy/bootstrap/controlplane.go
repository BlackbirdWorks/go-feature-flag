package bootstrap

// ControlPlane tells where the manifest lives and how it is polled.
type ControlPlane struct {
	Manifest   Manifest `json:"manifest,omitzero"  yaml:"manifest"`
	Connection string   `json:"connection"         yaml:"connection"`
	StateDir   string   `json:"stateDir,omitempty" yaml:"stateDir,omitempty"`
}

// Manifest locates and polls the manifest object.
type Manifest struct {
	PollInterval *Duration `json:"pollInterval,omitempty" yaml:"pollInterval,omitempty"`
	Jitter       *float64  `json:"jitter,omitempty"       jsonschema:"minimum=0,maximum=1,default=0.1" yaml:"jitter,omitempty"`     //nolint:lll // struct tags can not be wrapped
	MaxObjects   *int      `json:"maxObjects,omitempty"   jsonschema:"minimum=1,default=20000"         yaml:"maxObjects,omitempty"` //nolint:lll // struct tags can not be wrapped
	Key          string    `json:"key,omitempty"          yaml:"key"`
	Channel      string    `json:"channel,omitempty"      jsonschema:"default=stable"                  yaml:"channel,omitempty"` //nolint:lll // struct tags can not be wrapped
}

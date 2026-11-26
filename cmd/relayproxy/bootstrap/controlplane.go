package bootstrap

// ControlPlane tells where the manifest lives and how it is polled.
type ControlPlane struct {
	Manifest   Manifest `json:"manifest"           yaml:"manifest"`
	Connection string   `json:"connection"         yaml:"connection"`
	StateDir   string   `json:"stateDir,omitempty" yaml:"stateDir,omitempty"`
}

// Manifest locates and polls the manifest object.
type Manifest struct {
	PollInterval *Duration `json:"pollInterval,omitempty" yaml:"pollInterval,omitempty"`
	Jitter       *float64  `json:"jitter,omitempty"       yaml:"jitter,omitempty"`
	MaxObjects   *int      `json:"maxObjects,omitempty"   yaml:"maxObjects,omitempty"`
	Key          string    `json:"key"                    yaml:"key"`
	Channel      string    `json:"channel,omitempty"      yaml:"channel,omitempty"`
}

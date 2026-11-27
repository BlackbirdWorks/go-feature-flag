package bootstrap

// FileConnection is a connection of type file: a directory that holds the objects of the control plane, for a
// volume that is mounted into the relay (a local publish, a shared file system).
type FileConnection struct {
	Type string `json:"type" jsonschema:"enum=file" yaml:"type"`
	// Path is the root directory. Object keys are resolved below it and can not leave it.
	Path string `json:"path,omitempty" yaml:"path,omitempty"`
}

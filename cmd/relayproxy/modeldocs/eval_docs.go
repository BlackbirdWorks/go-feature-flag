package modeldocs

// EvalFlagDoc is the documentation struct for the Swagger doc.
type EvalFlagDoc struct {
	// The flag value for this user.
	Value any `json:"value"`
	// Metadata is a field containing information about your flag such as an issue tracker link, a description, etc ...
	Metadata *map[string]any `json:"metadata" toml:"metadata,omitempty" yaml:"metadata,omitempty"`
	// The variation used to give you this value.
	VariationType string `example:"variation-A" json:"variationType"`
	// The version of the flag used.
	Version string `example:"1.0" json:"version"`
	// reason why we have returned this value.
	Reason string `example:"TARGETING_MATCH" json:"reason"`
	// Code of the error returned by the server.
	ErrorCode string `example:"" json:"errorCode"`
	// `true` if the event was tracked by the relay proxy.
	TrackEvents bool `example:"true" json:"trackEvents"`
	// `true` if something went wrong in the relay proxy (flag does not exists, ...) and we serve the defaultValue.
	Failed bool `example:"false" json:"failed"`
}

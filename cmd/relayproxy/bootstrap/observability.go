package bootstrap

// Observability groups logging, metrics and tracing.
type Observability struct {
	Logging Logging `json:"logging,omitzero" yaml:"logging"`
	Metrics Metrics `json:"metrics,omitzero" yaml:"metrics"`
	Tracing Tracing `json:"tracing,omitzero" yaml:"tracing"`
}

// Logging is the slog setup.
type Logging struct {
	// Level is debug, info, warn or error. Default info.
	Level string `json:"level,omitempty" yaml:"level,omitempty"`
	// Format is json or text. Default json.
	Format string `json:"format,omitempty" yaml:"format,omitempty"`
}

// Metrics is the Prometheus setup.
type Metrics struct {
	// Enabled defaults to true.
	Enabled       *bool `json:"enabled,omitempty"       yaml:"enabled,omitempty"`
	LegacyNames   bool  `json:"legacyNames,omitempty"   yaml:"legacyNames,omitempty"`
	PerFlagLabels bool  `json:"perFlagLabels,omitempty" yaml:"perFlagLabels,omitempty"`
}

// Tracing is the OpenTelemetry setup.
type Tracing struct {
	OTLPEndpoint string `json:"otlpEndpoint,omitempty" yaml:"otlpEndpoint,omitempty"`
	// ServiceName defaults to semaphore.
	ServiceName string `json:"serviceName,omitempty" yaml:"serviceName,omitempty"`
	// Sampler defaults to parentbased_always_off.
	Sampler string `json:"sampler,omitempty" yaml:"sampler,omitempty"`
}

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
	Level string `json:"level,omitempty" jsonschema:"enum=debug,enum=info,enum=warn,enum=error,default=info" yaml:"level,omitempty"` //nolint:lll // struct tags can not be wrapped
	// Format is json or text. Default json.
	Format string `json:"format,omitempty" jsonschema:"enum=json,enum=text,default=json" yaml:"format,omitempty"`
}

// Metrics is the Prometheus setup.
type Metrics struct {
	// Enabled defaults to true.
	Enabled       *bool `json:"enabled,omitempty"       jsonschema:"default=true"      yaml:"enabled,omitempty"`
	LegacyNames   bool  `json:"legacyNames,omitempty"   yaml:"legacyNames,omitempty"`
	PerFlagLabels bool  `json:"perFlagLabels,omitempty" yaml:"perFlagLabels,omitempty"`
	// PerSegmentLabels adds the series labelled by segment name (off by default: the default series are labelled by
	// connection only).
	PerSegmentLabels bool `json:"perSegmentLabels,omitempty" yaml:"perSegmentLabels,omitempty"`
	// MaxSegmentLabels caps the distinct segment names of those series; the others are counted under "other".
	// Default 100.
	MaxSegmentLabels int32 `json:"maxSegmentLabels,omitempty" yaml:"maxSegmentLabels,omitempty"`
}

// Tracing is the OpenTelemetry setup.
type Tracing struct {
	OTLPEndpoint string `json:"otlpEndpoint,omitempty" yaml:"otlpEndpoint,omitempty"`
	// ServiceName defaults to semaphore.
	ServiceName string `json:"serviceName,omitempty" jsonschema:"default=semaphore" yaml:"serviceName,omitempty"`
	// Sampler defaults to parentbased_always_off.
	Sampler string `json:"sampler,omitempty" jsonschema:"enum=always_on,enum=always_off,enum=traceidratio,enum=parentbased_always_on,enum=parentbased_always_off,enum=parentbased_traceidratio,default=parentbased_always_off" yaml:"sampler,omitempty"` //nolint:lll // struct tags can not be wrapped
}

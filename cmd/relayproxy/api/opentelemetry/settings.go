package opentelemetry

// Settings is what the OpenTelemetry service reads of the configuration of the relay proxy. It is built once at
// startup and not changed afterwards.
type Settings struct {
	// ResourceAttributes are the attributes of the OpenTelemetry resource.
	ResourceAttributes map[string]string
	// Version is the version of the relay proxy, reported as the service version.
	Version string
	// OTLPEndpoint is the endpoint set by the openTelemetryOtlpEndpoint configuration element.
	OTLPEndpoint string
	// ExporterOTLPEndpoint is the endpoint set by otel.exporter.otlp.endpoint.
	ExporterOTLPEndpoint string
	// ServiceName is the name of the service, empty for the default.
	ServiceName string
	// Sampler is the trace sampler (OTEL_TRACES_SAMPLER), empty for always sample.
	Sampler string
	// JaegerSamplerManagerHostPort is the sampling server of the jaeger_remote sampler, empty for the default.
	JaegerSamplerManagerHostPort string
	// JaegerSamplerRefreshInterval is the refresh interval of the jaeger_remote sampler as a duration string.
	JaegerSamplerRefreshInterval string
	// JaegerSamplerMaxOperations is the maximum of operations of the jaeger_remote sampler, 0 for the default.
	JaegerSamplerMaxOperations int
	// SDKDisabled disables OpenTelemetry whatever the endpoint.
	SDKDisabled bool
}

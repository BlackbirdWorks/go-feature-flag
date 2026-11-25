package config

// OpenTelemetryConfiguration is the configuration for the OpenTelemetry part of the relay proxy
// It is used to configure the OpenTelemetry SDK and the OpenTelemetry Exporter
// Most of the time this configuration is set using environment variables.
type OpenTelemetryConfiguration struct {
	Resource OtelResource `koanf:"resource" mapstructure:"resource"`
	Exporter OtelExporter `koanf:"exporter" mapstructure:"exporter"`
	Service  struct {
		Name string `koanf:"name" mapstructure:"name"`
	} `koanf:"service" mapstructure:"service"`
	Traces struct {
		Sampler string `koanf:"sampler" mapstructure:"sampler"`
	} `koanf:"traces" mapstructure:"traces"`
	SDK struct {
		Disabled bool `koanf:"disabled" mapstructure:"disabled"`
	} `koanf:"sdk" mapstructure:"sdk"`
}

// OtelExporter is the configuration of the OpenTelemetry exporter.
type OtelExporter struct {
	Otlp OtelExporterOtlp `koanf:"otlp" mapstructure:"otlp"`
}

// OtelExporterOtlp is the configuration of the OTLP export of OpenTelemetry data.
type OtelExporterOtlp struct {
	Endpoint string `koanf:"endpoint" mapstructure:"endpoint"`
	Protocol string `koanf:"protocol" mapstructure:"protocol"`
}

// OtelResource is the resource attributes reported with OpenTelemetry data.
type OtelResource struct {
	Attributes map[string]string `koanf:"attributes" mapstructure:"attributes"`
}

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

type OtelExporter struct {
	Otlp OtelExporterOtlp `koanf:"otlp" mapstructure:"otlp"`
}

type OtelExporterOtlp struct {
	Endpoint string `koanf:"endpoint" mapstructure:"endpoint"`
	Protocol string `koanf:"protocol" mapstructure:"protocol"`
}

type OtelResource struct {
	Attributes map[string]string `koanf:"attributes" mapstructure:"attributes"`
}

// JaegerSamplerConfiguration is the configuration object to configure the sampling.
// Most of the time this configuration is set using environment variables.
type JaegerSamplerConfiguration struct {
	Sampler JaegerSampler `koanf:"sampler" mapstructure:"sampler"`
}

// JaegerSampler is the configuration object to configure the sampling.
type JaegerSampler struct {
	Manager JaegerSamplerManager `koanf:"manager" mapstructure:"manager"`
	Refresh JaegerSamplerRefresh `koanf:"refresh" mapstructure:"refresh"`
	Max     JaegerSamplerMax     `koanf:"max"     mapstructure:"max"`
}

// JaegerSamplerManager is the configuration object to configure the manager of the sampling.
type JaegerSamplerManager struct {
	Host JaegerSamplerManagerHost `koanf:"host" mapstructure:"host"`
}

// JaegerSamplerManagerHost is the configuration object to configure the host of the manager of the sampling.
type JaegerSamplerManagerHost struct {
	Port string `koanf:"port" mapstructure:"port"`
}

// JaegerSamplerRefresh is the configuration object to configure the refresh of the sampling.
type JaegerSamplerRefresh struct {
	Interval string `koanf:"interval" mapstructure:"interval"`
}

// JaegerSamplerMax is the configuration object to configure the max of the sampling.
type JaegerSamplerMax struct {
	Operations int `koanf:"operations" mapstructure:"operations"`
}

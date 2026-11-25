package config

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

// JaegerSamplerRefresh is the configuration object to configure the refresh of the sampling.
type JaegerSamplerRefresh struct {
	Interval string `koanf:"interval" mapstructure:"interval"`
}

// JaegerSamplerMax is the configuration object to configure the max of the sampling.
type JaegerSamplerMax struct {
	Operations int `koanf:"operations" mapstructure:"operations"`
}

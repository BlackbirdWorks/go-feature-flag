package config

// JaegerSamplerManager is the configuration object to configure the manager of the sampling.
type JaegerSamplerManager struct {
	Host JaegerSamplerManagerHost `koanf:"host" mapstructure:"host"`
}

// JaegerSamplerManagerHost is the configuration object to configure the host of the manager of the sampling.
type JaegerSamplerManagerHost struct {
	Port string `koanf:"port" mapstructure:"port"`
}

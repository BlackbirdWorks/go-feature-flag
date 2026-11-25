package config

import "log/slog"

const (
	// DefaultServerPort is the port the relay proxy listens on when none is configured.
	DefaultServerPort = 1031
)

type ServerMode = string

const (
	// ServerModeHTTP is the HTTP server mode.
	ServerModeHTTP ServerMode = "http"
	// ServerModeLambda is the AWS Lambda server mode.
	ServerModeLambda ServerMode = "lambda"
	// ServerModeUnixSocket is the Unix Socket server mode.
	ServerModeUnixSocket ServerMode = "unixsocket"
)

type LambdaAdapter = string

const (
	LambdaAdapterAPIGatewayV1 LambdaAdapter = "APIGatewayV1"
	LambdaAdapterAPIGatewayV2 LambdaAdapter = "APIGatewayV2"
	LambdaAdapterALB          LambdaAdapter = "ALB"
)

type Server struct {
	// Mode is the server mode.
	// default: http
	Mode ServerMode `koanf:"mode" mapstructure:"mode"`
	// Host is the server host.
	// default: 0.0.0.0
	Host string `koanf:"host" mapstructure:"host"`
	// UnixSocket is the server unix socket path.
	UnixSocketPath string `koanf:"unixsocketpath" mapstructure:"unixSocketPath"`
	// AWS Lambda configuration
	// LambdaAdapter is the adapter to use when the relay proxy is started as an AWS Lambda.
	// default: APIGatewayV2
	LambdaAdapter LambdaAdapter `koanf:"awsLambdaAdapter" mapstructure:"awsLambdaAdapter"`
	// AwsAPIGatewayBasePath (optional) is the base path prefix for AWS API Gateway deployments.
	// This is useful when deploying behind a non-root path like "/api" or "/dev/feature-flags".
	// The relay proxy will strip this base path from incoming requests before processing.
	// Example: if set to "/api/feature-flags", requests to "/api/feature-flags/health" will be processed as "/health"
	// Default: ""
	AwsAPIGatewayBasePath string `koanf:"awsapigatewaybasepath" mapstructure:"awsApiGatewayBasePath"`
	// Port is the server port.
	// default: 1031
	Port int `koanf:"port" mapstructure:"port"`
	// MonitoringPort is the monitoring port. It can be specified only if the server mode is HTTP.
	// default: none, it will use the same as server port.
	MonitoringPort int `koanf:"monitoringport" mapstructure:"monitoringPort"`
	// EnableH2C makes the HTTP server also accept HTTP/2 over cleartext TCP (h2c, prior knowledge), next to HTTP/1.1.
	// It is only used in http mode and is off by default.
	EnableH2C bool `koanf:"enableh2c" mapstructure:"enableH2C"`
}

// EffectiveMonitoringPort returns the monitoring port, checking first the top-level config
// and then the server config.
func (c *Config) EffectiveMonitoringPort(logger *slog.Logger) int {
	if c.Server.MonitoringPort != 0 {
		return c.Server.MonitoringPort
	}
	if c.MonitoringPort != 0 {
		if logger != nil {
			logger.Warn("The monitoring port is set using `monitoringPort`, " +
				"this option is deprecated, please migrate to `server.monitoringPort`")
		}

		return c.MonitoringPort
	}

	return 0
}

// ServerHost returns the server host, defaulting to "0.0.0.0" if not set.
func (c *Config) ServerHost() string {
	if c.Server.Host != "" {
		return c.Server.Host
	}

	return "0.0.0.0"
}

// ServerPort returns the server port, checking first the server config
// and then the top-level config, defaulting to 1031 if not set.
func (c *Config) ServerPort(logger *slog.Logger) int {
	if c.Server.Port != 0 {
		return c.Server.Port
	}

	if c.ListenPort != 0 {
		if logger != nil {
			logger.Warn(
				"The server port is set using `port`, this option is deprecated, please migrate to `server.port`",
			)
		}

		return c.ListenPort
	}

	return DefaultServerPort
}

// ServerMode returns the server mode, checking first the server config
// and then the top-level config, defaulting to HTTP if not set.
func (c *Config) ServerMode(logger *slog.Logger) ServerMode {
	if c.Server.Mode != "" {
		return c.Server.Mode
	}

	if c.StartAsAwsLambda {
		if logger != nil {
			logger.Warn("The server mode is set using `startAsAwsLambda`," +
				" this option is deprecated, please migrate to `server.mode`")
		}

		return ServerModeLambda
	}

	return ServerModeHTTP
}

// LambdaAdapter returns the lambda adapter, checking first the server config
// and then the top-level config, defaulting to APIGatewayV2 if not set.
func (c *Config) LambdaAdapter(logger *slog.Logger) LambdaAdapter {
	if c.Server.LambdaAdapter != "" {
		return c.Server.LambdaAdapter
	}

	if c.AwsLambdaAdapter != "" {
		if logger != nil {
			logger.Warn("The lambda adapter is set using `awsLambdaAdapter`," +
				" this option is deprecated, please migrate to `server.awsLambdaAdapter`")
		}

		return c.AwsLambdaAdapter
	}

	return LambdaAdapterAPIGatewayV2
}

// EffectiveAwsAPIGatewayBasePath returns the AWS API Gateway base path, checking first the server config
// and then the top-level config, defaulting to empty string if not set.
func (c *Config) EffectiveAwsAPIGatewayBasePath(logger *slog.Logger) string {
	if c.Server.AwsAPIGatewayBasePath != "" {
		return c.Server.AwsAPIGatewayBasePath
	}

	if c.AwsAPIGatewayBasePath != "" {
		if logger != nil {
			logger.Warn("The AWS API Gateway base path is set using `awsApiGatewayBasePath`, " +
				"this option is deprecated, please migrate to `server.awsApiGatewayBasePath`")
		}

		return c.AwsAPIGatewayBasePath
	}

	return ""
}

// UnixSocketPath returns the unix socket path.
func (c *Config) UnixSocketPath() string {
	return c.Server.UnixSocketPath
}

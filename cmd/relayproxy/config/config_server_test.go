package config_test

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	"github.com/blackbirdworks/semaphore/testutils/slogutil"
)

func TestConfig_EffectiveMonitoringPort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		config                *config.Config
		name                  string
		wantPort              int
		wantDeprecationWarned bool
		setLoggerNil          bool
	}{
		{
			name: "monitoring port from top-level config (deprecated)",
			config: &config.Config{
				MonitoringPort: 8080,
				Server: config.Server{
					MonitoringPort: 0,
				},
			},
			wantPort:              8080,
			wantDeprecationWarned: true,
		},
		{
			name: "monitoring port from server config",
			config: &config.Config{
				MonitoringPort: 0,
				Server: config.Server{
					MonitoringPort: 9090,
				},
			},
			wantPort:              9090,
			wantDeprecationWarned: false,
		},
		{
			name: "monitoring port from server takes precedence",
			config: &config.Config{
				MonitoringPort: 8080,
				Server: config.Server{
					MonitoringPort: 9090,
				},
			},
			wantPort:              9090,
			wantDeprecationWarned: false,
		},
		{
			name: "no monitoring port set",
			config: &config.Config{
				MonitoringPort: 0,
				Server: config.Server{
					MonitoringPort: 0,
				},
			},
			wantPort:              0,
			wantDeprecationWarned: false,
		},
		{
			name: "nil logger does not panic",
			config: &config.Config{
				MonitoringPort: 8080,
				Server: config.Server{
					MonitoringPort: 0,
				},
			},
			wantPort:              8080,
			wantDeprecationWarned: false,
			setLoggerNil:          true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var (
				logger       *slog.Logger
				observedLogs *slogutil.ObservedLogs
			)

			core, logs := slogutil.NewObserver(slog.LevelWarn)
			logger = core
			observedLogs = logs

			if tt.setLoggerNil {
				logger = nil
			}

			got := tt.config.EffectiveMonitoringPort(logger)
			assert.Equal(t, tt.wantPort, got)

			if observedLogs != nil && tt.wantDeprecationWarned {
				assert.Equal(t, 1, observedLogs.Len(), "expected deprecation warning")
				assert.Contains(t, observedLogs.All()[0].Message, "deprecated")
			} else if observedLogs != nil {
				assert.Equal(t, 0, observedLogs.Len(), "expected no deprecation warning")
			}
		})
	}
}

func TestConfig_ServerHost(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		config   *config.Config
		wantHost string
	}{
		{
			name: "host from server config",
			config: &config.Config{
				Server: config.Server{
					Host: "192.168.1.1",
				},
			},
			wantHost: "192.168.1.1",
		},
		{
			name: "empty host returns default",
			config: &config.Config{
				Server: config.Server{
					Host: "",
				},
			},
			wantHost: "0.0.0.0",
		},
		{
			name: "localhost",
			config: &config.Config{
				Server: config.Server{
					Host: "localhost",
				},
			},
			wantHost: "localhost",
		},
		{
			name: "custom domain",
			config: &config.Config{
				Server: config.Server{
					Host: "example.com",
				},
			},
			wantHost: "example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.config.ServerHost()
			assert.Equal(t, tt.wantHost, got)
		})
	}
}

func TestConfig_ServerPort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		config                *config.Config
		name                  string
		wantPort              int
		wantDeprecationWarned bool
		setLoggerNil          bool
	}{
		{
			name: "port from server config",
			config: &config.Config{
				Server: config.Server{
					Port: 8080,
				},
				ListenPort: 0,
			},
			wantPort:              8080,
			wantDeprecationWarned: false,
		},
		{
			name: "port from top-level ListenPort (deprecated)",
			config: &config.Config{
				Server: config.Server{
					Port: 0,
				},
				ListenPort: 9090,
			},
			wantPort:              9090,
			wantDeprecationWarned: true,
		},
		{
			name: "server port takes precedence over ListenPort",
			config: &config.Config{
				Server: config.Server{
					Port: 8080,
				},
				ListenPort: 9090,
			},
			wantPort:              8080,
			wantDeprecationWarned: false,
		},
		{
			name: "no port set returns default 1031",
			config: &config.Config{
				Server: config.Server{
					Port: 0,
				},
				ListenPort: 0,
			},
			wantPort:              1031,
			wantDeprecationWarned: false,
		},
		{
			name: "nil logger does not panic",
			config: &config.Config{
				Server: config.Server{
					Port: 8080,
				},
				ListenPort: 0,
			},
			wantPort:              8080,
			wantDeprecationWarned: false,
			setLoggerNil:          true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var (
				logger       *slog.Logger
				observedLogs *slogutil.ObservedLogs
			)

			core, logs := slogutil.NewObserver(slog.LevelWarn)
			logger = core
			observedLogs = logs

			if tt.setLoggerNil {
				logger = nil
			}

			got := tt.config.ServerPort(logger)
			assert.Equal(t, tt.wantPort, got)

			if observedLogs != nil && tt.wantDeprecationWarned {
				assert.Equal(t, 1, observedLogs.Len(), "expected deprecation warning")
				assert.Contains(t, observedLogs.All()[0].Message, "deprecated")
			} else if observedLogs != nil {
				assert.Equal(t, 0, observedLogs.Len(), "expected no deprecation warning")
			}
		})
	}
}

func TestConfig_ServerMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                  string
		config                *config.Config
		wantMode              config.ServerMode
		wantDeprecationWarned bool
	}{
		{
			name: "mode from server config - HTTP",
			config: &config.Config{
				Server: config.Server{
					Mode: config.ServerModeHTTP,
				},
			},
			wantMode:              config.ServerModeHTTP,
			wantDeprecationWarned: false,
		},
		{
			name: "mode from server config - Lambda",
			config: &config.Config{
				Server: config.Server{
					Mode: config.ServerModeLambda,
				},
				StartAsAwsLambda: false,
			},
			wantMode:              config.ServerModeLambda,
			wantDeprecationWarned: false,
		},
		{
			name: "mode from server config - UnixSocket",
			config: &config.Config{
				Server: config.Server{
					Mode: config.ServerModeUnixSocket,
				},
				StartAsAwsLambda: false,
			},
			wantMode:              config.ServerModeUnixSocket,
			wantDeprecationWarned: false,
		},
		{
			name: "mode from deprecated StartAsAwsLambda flag",
			config: &config.Config{
				Server: config.Server{
					Mode: "",
				},
				StartAsAwsLambda: true,
			},
			wantMode:              config.ServerModeLambda,
			wantDeprecationWarned: true,
		},
		{
			name: "server mode takes precedence over deprecated flag",
			config: &config.Config{
				Server: config.Server{
					Mode: config.ServerModeHTTP,
				},
				StartAsAwsLambda: true,
			},
			wantMode:              config.ServerModeHTTP,
			wantDeprecationWarned: false,
		},
		{
			name: "no mode set returns default HTTP",
			config: &config.Config{
				Server: config.Server{
					Mode: "",
				},
				StartAsAwsLambda: false,
			},
			wantMode:              config.ServerModeHTTP,
			wantDeprecationWarned: false,
		},
		{
			name: "nil logger does not panic",
			config: &config.Config{
				Server: config.Server{
					Mode: "",
				},
				StartAsAwsLambda: true,
			},
			wantMode:              config.ServerModeLambda,
			wantDeprecationWarned: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var (
				logger       *slog.Logger
				observedLogs *slogutil.ObservedLogs
			)

			if tt.wantDeprecationWarned {
				core, logs := slogutil.NewObserver(slog.LevelWarn)
				logger = core
				observedLogs = logs
			} else {
				logger = slog.New(slog.DiscardHandler)
			}

			got := tt.config.ServerMode(logger)
			assert.Equal(t, tt.wantMode, got)

			if observedLogs != nil && tt.wantDeprecationWarned {
				assert.GreaterOrEqual(t, observedLogs.Len(), 1, "expected deprecation warning")
				assert.Contains(t, observedLogs.All()[0].Message, "deprecated")
			}
		})
	}
}

func TestConfig_LambdaAdapter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                  string
		config                *config.Config
		wantAdapter           config.LambdaAdapter
		wantDeprecationWarned bool
	}{
		{
			name: "adapter from server config - APIGatewayV1",
			config: &config.Config{
				Server: config.Server{
					LambdaAdapter: config.LambdaAdapterAPIGatewayV1,
				},
				AwsLambdaAdapter: "",
			},
			wantAdapter:           config.LambdaAdapterAPIGatewayV1,
			wantDeprecationWarned: false,
		},
		{
			name: "adapter from server config - APIGatewayV2",
			config: &config.Config{
				Server: config.Server{
					LambdaAdapter: config.LambdaAdapterAPIGatewayV2,
				},
				AwsLambdaAdapter: "",
			},
			wantAdapter:           config.LambdaAdapterAPIGatewayV2,
			wantDeprecationWarned: false,
		},
		{
			name: "adapter from server config - ALB",
			config: &config.Config{
				Server: config.Server{
					LambdaAdapter: config.LambdaAdapterALB,
				},
				AwsLambdaAdapter: "",
			},
			wantAdapter:           config.LambdaAdapterALB,
			wantDeprecationWarned: false,
		},
		{
			name: "adapter from deprecated top-level config",
			config: &config.Config{
				Server: config.Server{
					LambdaAdapter: "",
				},
				AwsLambdaAdapter: "APIGatewayV1",
			},
			wantAdapter:           config.LambdaAdapterAPIGatewayV1,
			wantDeprecationWarned: true,
		},
		{
			name: "server adapter takes precedence over deprecated config",
			config: &config.Config{
				Server: config.Server{
					LambdaAdapter: config.LambdaAdapterALB,
				},
				AwsLambdaAdapter: "APIGatewayV1",
			},
			wantAdapter:           config.LambdaAdapterALB,
			wantDeprecationWarned: false,
		},
		{
			name: "no adapter set returns default APIGatewayV2",
			config: &config.Config{
				Server: config.Server{
					LambdaAdapter: "",
				},
				AwsLambdaAdapter: "",
			},
			wantAdapter:           config.LambdaAdapterAPIGatewayV2,
			wantDeprecationWarned: false,
		},
		{
			name: "nil logger does not panic",
			config: &config.Config{
				Server: config.Server{
					LambdaAdapter: "",
				},
				AwsLambdaAdapter: "APIGatewayV1",
			},
			wantAdapter:           config.LambdaAdapterAPIGatewayV1,
			wantDeprecationWarned: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var (
				logger       *slog.Logger
				observedLogs *slogutil.ObservedLogs
			)

			if tt.wantDeprecationWarned {
				core, logs := slogutil.NewObserver(slog.LevelWarn)
				logger = core
				observedLogs = logs
			} else {
				logger = slog.New(slog.DiscardHandler)
			}

			got := tt.config.LambdaAdapter(logger)
			assert.Equal(t, tt.wantAdapter, got)

			if observedLogs != nil && tt.wantDeprecationWarned {
				assert.GreaterOrEqual(t, observedLogs.Len(), 1, "expected deprecation warning")
				assert.Contains(t, observedLogs.All()[0].Message, "deprecated")
			}
		})
	}
}

func TestConfig_EffectiveAwsApiGatewayBasePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                  string
		config                *config.Config
		wantBasePath          string
		wantDeprecationWarned bool
	}{
		{
			name: "base path from server config",
			config: &config.Config{
				Server: config.Server{
					AwsAPIGatewayBasePath: "/api/v1",
				},
				AwsAPIGatewayBasePath: "",
			},
			wantBasePath:          "/api/v1",
			wantDeprecationWarned: false,
		},
		{
			name: "base path from deprecated top-level config",
			config: &config.Config{
				Server: config.Server{
					AwsAPIGatewayBasePath: "",
				},
				AwsAPIGatewayBasePath: "/legacy/path",
			},
			wantBasePath:          "/legacy/path",
			wantDeprecationWarned: true,
		},
		{
			name: "server base path takes precedence over deprecated config",
			config: &config.Config{
				Server: config.Server{
					AwsAPIGatewayBasePath: "/api/v2",
				},
				AwsAPIGatewayBasePath: "/api/v1",
			},
			wantBasePath:          "/api/v2",
			wantDeprecationWarned: false,
		},
		{
			name: "no base path set returns empty string",
			config: &config.Config{
				Server: config.Server{
					AwsAPIGatewayBasePath: "",
				},
				AwsAPIGatewayBasePath: "",
			},
			wantBasePath:          "",
			wantDeprecationWarned: false,
		},
		{
			name: "complex base path",
			config: &config.Config{
				Server: config.Server{
					AwsAPIGatewayBasePath: "/dev/feature-flags/v1",
				},
				AwsAPIGatewayBasePath: "",
			},
			wantBasePath:          "/dev/feature-flags/v1",
			wantDeprecationWarned: false,
		},
		{
			name: "nil logger does not panic",
			config: &config.Config{
				Server: config.Server{
					AwsAPIGatewayBasePath: "",
				},
				AwsAPIGatewayBasePath: "/api",
			},
			wantBasePath:          "/api",
			wantDeprecationWarned: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var (
				logger       *slog.Logger
				observedLogs *slogutil.ObservedLogs
			)

			if tt.wantDeprecationWarned {
				core, logs := slogutil.NewObserver(slog.LevelWarn)
				logger = core
				observedLogs = logs
			} else {
				logger = slog.New(slog.DiscardHandler)
			}

			got := tt.config.EffectiveAwsAPIGatewayBasePath(logger)
			assert.Equal(t, tt.wantBasePath, got)

			if observedLogs != nil && tt.wantDeprecationWarned {
				assert.GreaterOrEqual(t, observedLogs.Len(), 1, "expected deprecation warning")
				assert.Contains(t, observedLogs.All()[0].Message, "deprecated")
			}
		})
	}
}

func TestConfig_UnixSocketPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		config         *config.Config
		wantSocketPath string
	}{
		{
			name: "unix socket path set",
			config: &config.Config{
				Server: config.Server{
					UnixSocketPath: "/var/run/go-feature-flag.sock",
				},
			},
			wantSocketPath: "/var/run/go-feature-flag.sock",
		},
		{
			name: "empty unix socket path",
			config: &config.Config{
				Server: config.Server{
					UnixSocketPath: "",
				},
			},
			wantSocketPath: "",
		},
		{
			name: "unix socket path with relative path",
			config: &config.Config{
				Server: config.Server{
					UnixSocketPath: "./tmp/app.sock",
				},
			},
			wantSocketPath: "./tmp/app.sock",
		},
		{
			name: "unix socket path with tmp directory",
			config: &config.Config{
				Server: config.Server{
					UnixSocketPath: "/tmp/feature-flag.sock",
				},
			},
			wantSocketPath: "/tmp/feature-flag.sock",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.config.UnixSocketPath()
			assert.Equal(t, tt.wantSocketPath, got)
		})
	}
}

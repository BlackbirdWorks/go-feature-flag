package apiwire_test

import (
	"context"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api/opentelemetry"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/apiwire"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/handler/ofrep"
	controller "github.com/blackbirdworks/semaphore/cmd/relayproxy/handler/semaphore"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
)

const (
	basePath    = "/api"
	testVersion = "v1.2.3"
	anyHost     = "0.0.0.0"
)

func TestSettings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		conf *config.Config
		name string
		want api.Settings
	}{
		{
			name: "defaults",
			conf: &config.Config{Version: testVersion},
			want: api.Settings{
				Version:       testVersion,
				Mode:          api.ServerModeHTTP,
				Host:          anyHost,
				Port:          config.DefaultServerPort,
				LambdaAdapter: api.LambdaAdapterAPIGatewayV2,
				ValidateFlags: controller.ValidateFlagsLimits{},
				Telemetry:     opentelemetry.Settings{Version: testVersion},
			},
		},
		{
			name: "the deprecated options are resolved",
			conf: &config.Config{
				ListenPort:       8080,  //nolint:staticcheck // SA1019: the deprecated alias is resolved
				MonitoringPort:   8081,  //nolint:staticcheck // SA1019: the deprecated alias is resolved
				StartAsAwsLambda: true,  //nolint:staticcheck // SA1019: the deprecated alias is resolved
				AwsLambdaAdapter: "ALB", //nolint:staticcheck // SA1019: the deprecated alias is resolved
				EnableSwagger:    true,  //nolint:staticcheck // SA1019: the deprecated alias is resolved
				LogLevel:         "DEBUG",
			},
			want: api.Settings{
				Mode:           api.ServerModeLambda,
				Host:           anyHost,
				Port:           8080,
				MonitoringPort: 8081,
				LambdaAdapter:  api.LambdaAdapterALB,
				Swagger:        true,
				Debug:          true,
			},
		},
		{
			name: "every setting",
			conf: &config.Config{
				Version:                  "v2",
				EnablePprof:              true,
				ExposeLegacyMetrics:      true,
				DisableVersionHeader:     true,
				DisableSemaphoreMetadata: true,
				Server: config.Server{
					Mode: config.ServerModeUnixSocket, Host: "127.0.0.1", Port: 9, MonitoringPort: 10,
					UnixSocketPath: "/tmp/s.sock", LambdaAdapter: config.LambdaAdapterAPIGatewayV1,
					AwsAPIGatewayBasePath: basePath, EnableH2C: true,
				},
				OfrepEventStream: config.OfrepEventStream{BaseURL: "https://example.com", InactivityDelaySec: 5},
				Endpoints: config.Endpoints{ValidateFlags: config.ValidateFlagsEndpoint{
					MaxJSONBodySize: 1, MaxYAMLBodySize: 2, MaxTOMLBodySize: 3,
				}},
			},
			want: api.Settings{
				Version: "v2", Mode: api.ServerModeUnixSocket, Host: "127.0.0.1", Port: 9, MonitoringPort: 10,
				UnixSocketPath: "/tmp/s.sock", LambdaAdapter: api.LambdaAdapterAPIGatewayV1,
				AwsAPIGatewayBasePath: basePath, EnableH2C: true,
				Pprof: true, ExposeLegacyMetrics: true, DisableVersionHeader: true, DisableSemaphoreMetadata: true,
				OfrepEventStream: ofrep.EventStream{BaseURL: "https://example.com", InactivityDelaySec: 5},
				ValidateFlags: controller.ValidateFlagsLimits{
					MaxJSONBodySize: 1, MaxYAMLBodySize: 2, MaxTOMLBodySize: 3,
				},
				Telemetry: opentelemetry.Settings{Version: "v2"},
			},
		},
		{
			name: "an invalid event stream advertises nothing",
			conf: &config.Config{OfrepEventStream: config.OfrepEventStream{BaseURL: "ftp://example.com"}},
			want: api.Settings{
				Mode: api.ServerModeHTTP, Host: anyHost, Port: config.DefaultServerPort,
				LambdaAdapter: api.LambdaAdapterAPIGatewayV2,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := apiwire.Settings(tt.conf, slog.New(slog.DiscardHandler))
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestTelemetry(t *testing.T) {
	t.Parallel()

	conf := &config.Config{Version: "v3", OpenTelemetryOtlpEndpoint: "http://legacy:4318"}
	conf.OtelConfig.Exporter.Otlp.Endpoint = "http://exporter:4318"
	conf.OtelConfig.Service.Name = "svc"
	conf.OtelConfig.Traces.Sampler = "jaeger_remote"
	conf.OtelConfig.SDK.Disabled = true
	conf.OtelConfig.Resource.Attributes = map[string]string{"k": "v"}
	conf.JaegerConfig.Sampler.Manager.Host.Port = "host:5778"
	conf.JaegerConfig.Sampler.Refresh.Interval = "30s"
	conf.JaegerConfig.Sampler.Max.Operations = 7

	got := apiwire.Telemetry(conf)

	assert.Equal(t, opentelemetry.Settings{
		ResourceAttributes:           map[string]string{"k": "v"},
		Version:                      "v3",
		OTLPEndpoint:                 "http://legacy:4318",
		ExporterOTLPEndpoint:         "http://exporter:4318",
		ServiceName:                  "svc",
		Sampler:                      "jaeger_remote",
		JaegerSamplerManagerHostPort: "host:5778",
		JaegerSamplerRefreshInterval: "30s",
		JaegerSamplerMaxOperations:   7,
		SDKDisabled:                  true,
	}, got)

	// every field is set by the fixture: a field added to the settings without a mapping fails here, and the
	// internal tests of the opentelemetry package pin the same mapping with the same fixture (settingsOf).
	for i := range reflect.TypeFor[opentelemetry.Settings]().NumField() {
		field := reflect.TypeFor[opentelemetry.Settings]().Field(i)
		assert.False(t, reflect.ValueOf(got).Field(i).IsZero(), "the fixture sets %s", field.Name)
	}

	// the settings do not share the attributes of the configuration, a later change of the configuration is not seen.
	conf.OtelConfig.Resource.Attributes["k"] = "changed"

	assert.Equal(t, "v", got.ResourceAttributes["k"])
}

// TestDefaultsMatchTheConfiguration checks that the constants that the HTTP layer owns are the ones of the
// configuration, whose documented defaults and names they replace.
func TestDefaultsMatchTheConfiguration(t *testing.T) {
	t.Parallel()

	assert.Equal(t, config.ServerModeHTTP, api.ServerModeHTTP)
	assert.Equal(t, config.ServerModeLambda, api.ServerModeLambda)
	assert.Equal(t, config.ServerModeUnixSocket, api.ServerModeUnixSocket)
	assert.Equal(t, config.LambdaAdapterAPIGatewayV1, api.LambdaAdapterAPIGatewayV1)
	assert.Equal(t, config.LambdaAdapterAPIGatewayV2, api.LambdaAdapterAPIGatewayV2)
	assert.Equal(t, config.LambdaAdapterALB, api.LambdaAdapterALB)
	assert.Equal(t, controller.DefaultMaxJSONBodySize, int64(config.DefaultValidateFlagsMaxJSONBodySize))
	assert.Equal(t, controller.DefaultMaxYAMLBodySize, int64(config.DefaultValidateFlagsMaxYAMLBodySize))
	assert.Equal(t, controller.DefaultMaxTOMLBodySize, int64(config.DefaultValidateFlagsMaxTOMLBodySize))
}

func TestNew(t *testing.T) {
	t.Parallel()

	conf := &config.Config{AuthorizedKeys: config.APIKeys{Admin: []string{"admin"}}}
	conf.ForceReloadAPIKeys()

	srv := apiwire.New(conf, &service.Services{}, slog.New(slog.DiscardHandler))

	assert.NotNil(t, srv.Handler())
}

// recordingHandler records the message of every record it handles.
type recordingHandler struct {
	messages *[]string
}

func (recordingHandler) Enabled(context.Context, slog.Level) bool { return true }

//nolint:gocritic // hugeParam: the signature is the one of slog.Handler
func (h recordingHandler) Handle(_ context.Context, r slog.Record) error {
	*h.messages = append(*h.messages, r.Message)

	return nil
}

func (h recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h recordingHandler) WithGroup(string) slog.Handler      { return h }

// TestSettings_DeprecationWarnings checks which deprecated options warn in which server mode, and that each warns
// once: the relay proxy only warned about the options that its mode used.
func TestSettings_DeprecationWarnings(t *testing.T) {
	t.Parallel()

	const (
		warnMode      = "The server mode is set using `startAsAwsLambda`"
		warnPort      = "The server port is set using `port`"
		warnMonitor   = "The monitoring port is set using `monitoringPort`"
		warnAdapter   = "The lambda adapter is set using `awsLambdaAdapter`"
		warnBasePath  = "The AWS API Gateway base path is set using `awsApiGatewayBasePath`"
		deprecatedFmt = "%s: the message starts with %q"
	)

	// every option that has a deprecated name is set with it.
	deprecated := func(server config.Server, startAsLambda bool) *config.Config {
		return &config.Config{
			Server:                server,
			StartAsAwsLambda:      startAsLambda, //nolint:staticcheck // SA1019: the test covers the deprecated setting
			ListenPort:            8080,          //nolint:staticcheck // SA1019: the test covers the deprecated setting
			MonitoringPort:        8081,          //nolint:staticcheck // SA1019: the test covers the deprecated setting
			AwsLambdaAdapter:      "ALB",         //nolint:staticcheck // SA1019: the test covers the deprecated setting
			AwsAPIGatewayBasePath: basePath,      //nolint:staticcheck // SA1019: the test covers the deprecated setting
		}
	}

	tests := []struct {
		conf         *config.Config
		name         string
		wantWarnings []string
	}{
		{
			name:         "http mode: the port and the monitoring port",
			conf:         deprecated(config.Server{Mode: config.ServerModeHTTP}, false),
			wantWarnings: []string{warnPort, warnMonitor},
		},
		{
			name:         "the default mode is http",
			conf:         deprecated(config.Server{}, false),
			wantWarnings: []string{warnPort, warnMonitor},
		},
		{
			name:         "lambda mode: the adapter, the base path and the monitoring port",
			conf:         deprecated(config.Server{Mode: config.ServerModeLambda}, false),
			wantWarnings: []string{warnAdapter, warnBasePath, warnMonitor},
		},
		{
			name:         "unix socket mode: the monitoring port",
			conf:         deprecated(config.Server{Mode: config.ServerModeUnixSocket}, false),
			wantWarnings: []string{warnMonitor},
		},
		{
			name:         "the deprecated mode is lambda: the mode, the adapter, the base path and the monitoring port",
			conf:         deprecated(config.Server{}, true),
			wantWarnings: []string{warnMode, warnAdapter, warnBasePath, warnMonitor},
		},
		{
			name:         "nothing deprecated, nothing logged",
			conf:         &config.Config{Server: config.Server{Port: 1, MonitoringPort: 2}},
			wantWarnings: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var messages []string

			apiwire.Settings(tt.conf, slog.New(recordingHandler{messages: &messages}))

			require.Len(t, messages, len(tt.wantWarnings))

			for _, want := range tt.wantWarnings {
				count := 0

				for _, got := range messages {
					if strings.HasPrefix(got, want) {
						count++
					}
				}

				assert.Equal(t, 1, count, deprecatedFmt, tt.name, want)
			}
		})
	}
}

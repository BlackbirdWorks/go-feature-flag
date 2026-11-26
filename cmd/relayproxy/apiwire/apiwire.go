package apiwire

import (
	"log/slog"
	"maps"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api/opentelemetry"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/handler/ofrep"
	controller "github.com/blackbirdworks/semaphore/cmd/relayproxy/handler/semaphore"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
)

// Settings copies what the HTTP layer reads of the configuration. The defaults and the deprecated aliases are
// resolved here, once. The logger receives the deprecation warning of an option that is set with a deprecated name
// (nil logs nothing), and only when the relay proxy uses the option: the server mode and the monitoring port always,
// the port in the http mode, the Lambda adapter and the base path in the lambda mode. The values of the other modes
// are resolved silently.
func Settings(cfg *config.Config, logger *slog.Logger) api.Settings {
	mode := cfg.ServerMode(logger)

	var httpLogger, lambdaLogger *slog.Logger

	switch mode {
	case config.ServerModeLambda:
		lambdaLogger = logger
	case config.ServerModeUnixSocket:
		// no mode specific option has a deprecated name
	default:
		httpLogger = logger
	}

	return api.Settings{
		Telemetry:                Telemetry(cfg),
		OfrepEventStream:         eventStream(cfg.OfrepEventStream),
		ValidateFlags:            validateFlagsLimits(cfg.Endpoints.ValidateFlags),
		Version:                  cfg.Version,
		Mode:                     mode,
		Host:                     cfg.ServerHost(),
		UnixSocketPath:           cfg.UnixSocketPath(),
		LambdaAdapter:            cfg.LambdaAdapter(lambdaLogger),
		AwsAPIGatewayBasePath:    cfg.EffectiveAwsAPIGatewayBasePath(lambdaLogger),
		Port:                     cfg.ServerPort(httpLogger),
		MonitoringPort:           cfg.EffectiveMonitoringPort(logger),
		EnableH2C:                cfg.Server.EnableH2C,
		Debug:                    cfg.IsDebugEnabled(),
		Swagger:                  cfg.IsSwaggerEnabled(),
		Pprof:                    cfg.EnablePprof,
		ExposeLegacyMetrics:      cfg.ExposeLegacyMetrics,
		DisableVersionHeader:     cfg.DisableVersionHeader,
		DisableSemaphoreMetadata: cfg.DisableSemaphoreMetadata,
	}
}

// Telemetry copies the OpenTelemetry settings of the configuration.
func Telemetry(cfg *config.Config) opentelemetry.Settings {
	return opentelemetry.Settings{
		ResourceAttributes:           maps.Clone(cfg.OtelConfig.Resource.Attributes),
		Version:                      cfg.Version,
		OTLPEndpoint:                 cfg.OpenTelemetryOtlpEndpoint,
		ExporterOTLPEndpoint:         cfg.OtelConfig.Exporter.Otlp.Endpoint,
		ServiceName:                  cfg.OtelConfig.Service.Name,
		Sampler:                      cfg.OtelConfig.Traces.Sampler,
		JaegerSamplerManagerHostPort: cfg.JaegerConfig.Sampler.Manager.Host.Port,
		JaegerSamplerRefreshInterval: cfg.JaegerConfig.Sampler.Refresh.Interval,
		JaegerSamplerMaxOperations:   cfg.JaegerConfig.Sampler.Max.Operations,
		SDKDisabled:                  cfg.OtelConfig.SDK.Disabled,
	}
}

// New builds the API server of the configuration: its settings are copied now, its API keys follow the
// configuration when it reloads.
func New(cfg *config.Config, services *service.Services, logger *slog.Logger, opts ...api.Option) api.Server {
	settings := Settings(cfg, logger)

	return api.New(&settings, service.NewAPIKeys(cfg), services, logger, opts...)
}

// eventStream is the OFREP event stream of the configuration, or the zero value (nothing advertised) when the
// configuration does not enable it or it is not valid.
func eventStream(es config.OfrepEventStream) ofrep.EventStream {
	if !es.IsEnabled() {
		return ofrep.EventStream{}
	}

	stream, err := ofrep.NewEventStream(es.BaseURL, es.InactivityDelaySec)
	if err != nil {
		// the configuration validation refuses it at startup, nothing is advertised if it got here.
		return ofrep.EventStream{}
	}

	return stream
}

func validateFlagsLimits(v config.ValidateFlagsEndpoint) controller.ValidateFlagsLimits {
	return controller.ValidateFlagsLimits{
		MaxJSONBodySize: int64(v.MaxJSONBodySize),
		MaxYAMLBodySize: int64(v.MaxYAMLBodySize),
		MaxTOMLBodySize: int64(v.MaxTOMLBodySize),
	}
}

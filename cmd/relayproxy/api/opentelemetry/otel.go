package opentelemetry

import (
	"context"
	"fmt"
	"os"
	"time"

	"go.opentelemetry.io/contrib/exporters/autoexport"
	"go.opentelemetry.io/contrib/samplers/jaegerremote"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace/noop"
	"go.uber.org/zap"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
)

type OtelService struct {
	otelTraceProvider *sdktrace.TracerProvider
	otelExporter      sdktrace.SpanExporter
}

func NewOtelService() OtelService {
	return OtelService{}
}

// isDisabled is true when OpenTelemetry has no endpoint to send to, or is disabled.
// The endpoint is set either by the openTelemetryOtlpEndpoint config element or otel.exporter.otlp.endpoint.
func isDisabled(config *config.Config) bool {
	return (config.OpenTelemetryOtlpEndpoint == "" && config.OtelConfig.Exporter.Otlp.Endpoint == "") ||
		config.OtelConfig.SDK.Disabled
}

// ExportEnvironment gives the OTLP endpoint of the configuration to the OpenTelemetry SDK, which reads it
// from OTEL_EXPORTER_OTLP_ENDPOINT. It changes the environment of the process, so it belongs to the
// startup wiring (main): call it once, before Init.
func ExportEnvironment(config *config.Config) {
	if isDisabled(config) {
		return
	}
	// support the openTelemetryOtlpEndpoint config element
	if config.OpenTelemetryOtlpEndpoint != "" &&
		config.OtelConfig.Exporter.Otlp.Endpoint == "" {
		config.OtelConfig.Exporter.Otlp.Endpoint = config.OpenTelemetryOtlpEndpoint
		_ = os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", config.OpenTelemetryOtlpEndpoint)
	} else if config.OtelConfig.Exporter.Otlp.Endpoint != "" && os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" {
		_ = os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", config.OtelConfig.Exporter.Otlp.Endpoint)
	}
}

// Init creates the OpenTelemetry tracer provider of the service. It does not change any process-wide
// state: Install does, and is called by the startup wiring only.
func (s *OtelService) Init(ctx context.Context, config *config.Config) error {
	if isDisabled(config) {
		return nil
	}

	exporter, err := autoexport.NewSpanExporter(ctx)
	if err != nil {
		return fmt.Errorf("initializing OTel exporter: %w", err)
	}

	serviceName := "semaphore"
	if v := config.OtelConfig.Service.Name; v != "" {
		serviceName = v
	}

	sampler, err := initSampler(serviceName, config)
	if err != nil {
		return fmt.Errorf("initializing OTel sampler: %w", err)
	}

	resource, err := initResource(
		ctx,
		serviceName,
		config.Version,
		config.OtelConfig.Resource.Attributes,
	)
	if err != nil {
		return fmt.Errorf("initializing OTel resources: %w", err)
	}

	s.otelExporter = exporter
	s.otelTraceProvider = sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithSampler(sampler),
		sdktrace.WithResource(resource),
	)

	return nil
}

// Install makes the tracer provider of the service (or a no-op one when Init found OpenTelemetry disabled)
// the global OpenTelemetry provider, with the error handler and the propagator of the relay proxy.
// The OpenTelemetry API only offers process-wide registration, so it is the startup wiring (main) that
// asks for it.
func (s *OtelService) Install(zapLog *zap.Logger) {
	if s.otelTraceProvider == nil {
		otel.SetTracerProvider(noop.NewTracerProvider())

		return
	}
	otel.SetTracerProvider(s.otelTraceProvider)
	otel.SetErrorHandler(newErrorHandler(zapLog))
	otel.SetTextMapPropagator(newPropagator())
}

// newErrorHandler logs the OTel errors to zap rather than the default log package.
func newErrorHandler(zapLog *zap.Logger) otel.ErrorHandler {
	return otelErrHandler(func(err error) {
		zapLog.Error("OTel error", zap.Error(err))
	})
}

// newPropagator is the propagator of the relay proxy: W3C trace context and baggage.
func newPropagator() propagation.TextMapPropagator {
	return propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	)
}

type otelErrHandler func(err error)

func (o otelErrHandler) Handle(err error) {
	o(err)
}

var _ otel.ErrorHandler = otelErrHandler(nil)

func initResource(ctx context.Context, serviceName, version string,
	attribs map[string]string) (*resource.Resource, error) {
	attrs := make([]attribute.KeyValue, 0, len(attribs))
	for k, v := range attribs {
		attrs = append(attrs, attribute.String(k, v))
	}

	return resource.New(ctx,
		resource.WithAttributes(attrs...),
		resource.WithProcessPID(),
		resource.WithProcessExecutableName(),
		resource.WithProcessExecutablePath(),
		resource.WithProcessOwner(),
		resource.WithProcessRuntimeName(),
		resource.WithProcessRuntimeVersion(),
		resource.WithProcessRuntimeDescription(),
		resource.WithHost(),
		resource.WithTelemetrySDK(),
		resource.WithOS(),
		resource.WithContainer(),
		resource.WithAttributes(
			semconv.ServiceNameKey.String(serviceName),
			semconv.ServiceVersionKey.String(version),
		),
	)
}

// initSampler determines which sampling strategy to use. If OTEL_TRACES_SAMPLER
// is unset, we'll always sample.
// If it's set to jaeger_remote, we'll use the Jaeger sampling server (supports
// JAEGER_SAMPLER_MANAGER_HOST_PORT, JAEGER_SAMPLER_REFRESH_INTERVAL, and
// JAEGER_SAMPLER_MAX_OPERATIONS).
// If it's set to any other value, we return nil and sdktrace.NewTracerProvider
// will set up the initSampler from the environment.
func initSampler(serviceName string, conf *config.Config) (sdktrace.Sampler, error) {
	sampler := conf.OtelConfig.Traces.Sampler
	if sampler == "" {
		return sdktrace.AlwaysSample(), nil
	}

	if sampler != "jaeger_remote" {
		//nolint:nilnil // a nil sampler means "no sampler configured here"; the caller falls back to its default
		return nil, nil
	}

	samplerURL, samplerRefreshInterval, maxOperations, err := jaegerRemoteSamplerOpts(conf)
	if err != nil {
		return nil, err
	}

	return jaegerremote.New(
		serviceName,
		jaegerremote.WithSamplingServerURL(samplerURL),
		jaegerremote.WithSamplingRefreshInterval(samplerRefreshInterval),
		jaegerremote.WithMaxOperations(maxOperations),
		jaegerremote.WithInitialSampler(sdktrace.AlwaysSample()),
	), nil
}

const (
	defaultSamplerURL              = "http://localhost:5778/sampling"
	defaultSamplingRefreshInterval = 1 * time.Minute
	defaultSamplingMaxOperations   = 256
)

func jaegerRemoteSamplerOpts(conf *config.Config) (string, time.Duration, int, error) {
	samplerURL := defaultSamplerURL
	if host := conf.JaegerConfig.Sampler.Manager.Host.Port; host != "" {
		samplerURL = host
	}

	samplerRefreshInterval := defaultSamplingRefreshInterval
	if v := conf.JaegerConfig.Sampler.Refresh.Interval; v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return "", 0, 0, fmt.Errorf("parsing JAEGER_SAMPLER_REFRESH_INTERVAL: %w", err)
		}
		samplerRefreshInterval = d
	}

	maxOperations := defaultSamplingMaxOperations
	if v := conf.JaegerConfig.Sampler.Max.Operations; v != 0 {
		maxOperations = v
	}

	return samplerURL, samplerRefreshInterval, maxOperations, nil
}

// Stop the OpenTelemetry service.
func (s *OtelService) Stop(ctx context.Context) error {
	if s.otelExporter != nil {
		err := s.otelExporter.Shutdown(ctx)
		if err != nil {
			return err
		}
	}

	if s.otelTraceProvider != nil {
		err := s.otelTraceProvider.Shutdown(ctx)
		if err != nil {
			return err
		}
	}

	return nil
}

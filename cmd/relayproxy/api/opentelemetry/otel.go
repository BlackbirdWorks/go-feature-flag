package opentelemetry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"go.opentelemetry.io/contrib/exporters/autoexport"
	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/contrib/samplers/jaegerremote"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/blackbirdworks/semaphore/cmdhelpers/cmdlog"
)

// OtelService owns the OpenTelemetry providers of the relay proxy: the tracer provider and, with the same
// configuration, the meter provider (Go runtime metrics, and the HTTP server metrics of the requests).
type OtelService struct {
	otelTraceProvider *sdktrace.TracerProvider
	otelExporter      sdktrace.SpanExporter
	meterProvider     *sdkmetric.MeterProvider
	// metricReader replaces the reader built from the OTEL_* environment, see WithMetricReader.
	metricReader sdkmetric.Reader
}

// Option customizes an OtelService.
type Option func(*OtelService)

// WithMetricReader makes the metrics go to the reader instead of the exporter that the OTEL_* environment
// variables select. It is for the tests, which read the metrics themselves.
func WithMetricReader(reader sdkmetric.Reader) Option {
	return func(s *OtelService) { s.metricReader = reader }
}

// NewOtelService returns an OtelService, with no provider: Init creates them.
func NewOtelService(opts ...Option) OtelService {
	s := OtelService{}
	for _, opt := range opts {
		opt(&s)
	}

	return s
}

// MeterProvider returns the meter provider of the service, nil when OpenTelemetry is not configured or InitMetrics
// was not called.
func (s *OtelService) MeterProvider() *sdkmetric.MeterProvider { return s.meterProvider }

// Enabled reports whether the configuration enables OpenTelemetry tracing: without an OTLP endpoint there is no
// exporter, and nothing is traced.
func Enabled(cfg *Settings) bool { return !isDisabled(cfg) }

// isDisabled is true when OpenTelemetry has no endpoint to send to, or is disabled.
// The endpoint is set either by the openTelemetryOtlpEndpoint config element or otel.exporter.otlp.endpoint.
func isDisabled(cfg *Settings) bool {
	return (cfg.OTLPEndpoint == "" && cfg.ExporterOTLPEndpoint == "") || cfg.SDKDisabled
}

// ExportEnvironment gives the OTLP endpoint of the configuration to the OpenTelemetry SDK, which reads it
// from OTEL_EXPORTER_OTLP_ENDPOINT. It changes the environment of the process, so it belongs to the
// startup wiring (main): call it once, before Init.
func ExportEnvironment(cfg *Settings) {
	if isDisabled(cfg) {
		return
	}
	// support the openTelemetryOtlpEndpoint config element
	if cfg.OTLPEndpoint != "" && cfg.ExporterOTLPEndpoint == "" {
		_ = os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", cfg.OTLPEndpoint)
	} else if cfg.ExporterOTLPEndpoint != "" && os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" {
		_ = os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", cfg.ExporterOTLPEndpoint)
	}
}

// Init creates the OpenTelemetry tracer provider of the service. It does not change any process-wide
// state: Install does, and is called by the startup wiring only.
func (s *OtelService) Init(ctx context.Context, cfg *Settings) error {
	if isDisabled(cfg) {
		return nil
	}

	exporter, err := autoexport.NewSpanExporter(ctx)
	if err != nil {
		return fmt.Errorf("initializing OTel exporter: %w", err)
	}

	serviceName := "semaphore"
	if v := cfg.ServiceName; v != "" {
		serviceName = v
	}

	sampler, err := initSampler(serviceName, cfg)
	if err != nil {
		return fmt.Errorf("initializing OTel sampler: %w", err)
	}

	res, err := initResource(
		ctx,
		serviceName,
		cfg.Version,
		cfg.ResourceAttributes,
	)
	if err != nil {
		return fmt.Errorf("initializing OTel resources: %w", err)
	}

	s.otelExporter = exporter

	tpOpts := []sdktrace.TracerProviderOption{
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	}
	if sampler.sampler != nil {
		tpOpts = append(tpOpts, sdktrace.WithSampler(sampler.sampler))
	}

	s.otelTraceProvider = sdktrace.NewTracerProvider(tpOpts...)

	return nil
}

// InitMetrics creates the OpenTelemetry meter provider of the service and starts the Go runtime metrics on it, when
// OpenTelemetry is configured (same condition as the tracer provider). The metrics are exported like the traces:
// the protocol, the endpoint and the headers follow the OTEL_EXPORTER_OTLP_* variables, and OTEL_METRICS_EXPORTER
// selects the exporter (none disables the export). The export interval is the one of the SDK.
// It changes no process-wide state: the meter provider is given where it is used (MeterProvider).
func (s *OtelService) InitMetrics(ctx context.Context, cfg *Settings) error {
	if isDisabled(cfg) {
		return nil
	}

	serviceName := "semaphore"
	if v := cfg.ServiceName; v != "" {
		serviceName = v
	}

	res, err := initResource(ctx, serviceName, cfg.Version, cfg.ResourceAttributes)
	if err != nil {
		return fmt.Errorf("initializing OTel resources: %w", err)
	}

	reader := s.metricReader
	if reader == nil {
		reader, err = autoexport.NewMetricReader(ctx)
		if err != nil {
			return fmt.Errorf("initializing OTel metric exporter: %w", err)
		}
	}

	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader), sdkmetric.WithResource(res))

	err = runtime.Start(runtime.WithMeterProvider(provider))
	if err != nil {
		_ = provider.Shutdown(ctx)

		return fmt.Errorf("starting the Go runtime metrics: %w", err)
	}

	s.meterProvider = provider

	return nil
}

// Install makes the tracer provider of the service (or a no-op one when Init found OpenTelemetry disabled)
// the global OpenTelemetry provider, with the error handler and the propagator of the relay proxy.
// The OpenTelemetry API only offers process-wide registration, so it is the startup wiring (main) that
// asks for it.
func (s *OtelService) Install(ctx context.Context, logger *slog.Logger) {
	if s.otelTraceProvider == nil {
		otel.SetTracerProvider(noop.NewTracerProvider())

		return
	}

	otel.SetTracerProvider(s.otelTraceProvider)
	otel.SetErrorHandler(newErrorHandler(ctx, logger))
	otel.SetTextMapPropagator(newPropagator())
}

// newErrorHandler logs the OTel errors rather than the default log package. The handler has no context of its
// own: it logs with the values of ctx, and keeps logging once ctx is cancelled (the errors of the shutdown).
func newErrorHandler(ctx context.Context, logger *slog.Logger) otel.ErrorHandlerFunc {
	logCtx := context.WithoutCancel(ctx)

	return otel.ErrorHandlerFunc(func(err error) {
		logger.LogAttrs(logCtx, slog.LevelError, "OTel error", cmdlog.Err(err))
	})
}

// textMapPropagator wraps [propagation.TextMapPropagator] so newPropagator returns a concrete type.
type textMapPropagator struct {
	propagation.TextMapPropagator
}

// newPropagator is the propagator of the relay proxy: W3C trace context and baggage.
func newPropagator() textMapPropagator {
	return textMapPropagator{
		TextMapPropagator: propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		),
	}
}

func initResource(ctx context.Context, serviceName, version string,
	attribs map[string]string) (*resource.Resource, error) {
	attrs := make([]attribute.KeyValue, 0, len(attribs))
	for k, v := range attribs {
		attrs = append(attrs, attribute.String(k, v))
	}

	res, err := resource.New(ctx,
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
	if err != nil {
		return nil, fmt.Errorf("create OpenTelemetry resource: %w", err)
	}

	return res, nil
}

// traceSampler holds an optional [sdktrace.Sampler] selected at startup.
type traceSampler struct {
	sampler sdktrace.Sampler
}

// initSampler determines which sampling strategy to use. If OTEL_TRACES_SAMPLER
// is unset, we'll always sample.
// If it's set to jaeger_remote, we'll use the Jaeger sampling server (supports
// JAEGER_SAMPLER_MANAGER_HOST_PORT, JAEGER_SAMPLER_REFRESH_INTERVAL, and
// JAEGER_SAMPLER_MAX_OPERATIONS).
// If it's set to any other value, we return an empty traceSampler and sdktrace.NewTracerProvider
// will set up the sampler from the environment.
func initSampler(serviceName string, conf *Settings) (traceSampler, error) {
	sampler := conf.Sampler
	if sampler == "" {
		return traceSampler{sampler: sdktrace.AlwaysSample()}, nil
	}

	if sampler != "jaeger_remote" {
		return traceSampler{}, nil
	}

	samplerOpts, err := jaegerRemoteSamplerOpts(conf)
	if err != nil {
		return traceSampler{}, err
	}

	return traceSampler{
		sampler: jaegerremote.New(
			serviceName,
			jaegerremote.WithSamplingServerURL(samplerOpts.url),
			jaegerremote.WithSamplingRefreshInterval(samplerOpts.refreshInterval),
			jaegerremote.WithMaxOperations(samplerOpts.maxOperations),
			jaegerremote.WithInitialSampler(sdktrace.AlwaysSample()),
		),
	}, nil
}

const (
	defaultSamplerURL              = "http://localhost:5778/sampling"
	defaultSamplingRefreshInterval = 1 * time.Minute
	defaultSamplingMaxOperations   = 256
)

// jaegerSamplerOptions are the settings of the Jaeger remote sampler.
type jaegerSamplerOptions struct {
	url             string
	refreshInterval time.Duration
	maxOperations   int
}

func jaegerRemoteSamplerOpts(conf *Settings) (jaegerSamplerOptions, error) {
	samplerURL := defaultSamplerURL
	if host := conf.JaegerSamplerManagerHostPort; host != "" {
		samplerURL = host
	}

	samplerRefreshInterval := defaultSamplingRefreshInterval

	if v := conf.JaegerSamplerRefreshInterval; v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return jaegerSamplerOptions{}, fmt.Errorf("parsing JAEGER_SAMPLER_REFRESH_INTERVAL: %w", err)
		}

		samplerRefreshInterval = d
	}

	maxOperations := defaultSamplingMaxOperations
	if v := conf.JaegerSamplerMaxOperations; v != 0 {
		maxOperations = v
	}

	return jaegerSamplerOptions{
		url:             samplerURL,
		refreshInterval: samplerRefreshInterval,
		maxOperations:   maxOperations,
	}, nil
}

// Stop the OpenTelemetry service. The exporter, the tracer provider and the meter provider are all shut down, whatever
// the failure of one of them (the metrics are flushed within the deadline of ctx even when the traces fail to stop);
// the errors are joined.
func (s *OtelService) Stop(ctx context.Context) error {
	var errs []error
	if s.otelExporter != nil {
		errs = append(errs, s.otelExporter.Shutdown(ctx))
	}

	if s.otelTraceProvider != nil {
		errs = append(errs, s.otelTraceProvider.Shutdown(ctx))
	}
	// Shutdown flushes the metrics that were not exported yet.
	if s.meterProvider != nil {
		err := s.meterProvider.Shutdown(ctx)
		if err != nil {
			errs = append(errs, fmt.Errorf("stopping the OTel meter provider: %w", err))
		}
	}

	return errors.Join(errs...)
}

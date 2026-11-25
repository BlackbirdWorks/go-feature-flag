package opentelemetry

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	"github.com/blackbirdworks/semaphore/testutils/slogutil"
)

var (
	errTestError = errors.New("test error")
)

// T.Setenv: the OpenTelemetry and Jaeger SDKs (third-party code) read OTEL_*/JAEGER_* from the process environment.
//
//nolint:paralleltest // see the comment above
func TestInitSampler(t *testing.T) {
	t.Run("OTEL_TRACES_SAMPLER unset", func(t *testing.T) {
		f := pflag.NewFlagSet("config", pflag.ContinueOnError)
		c, errC := config.New(f, slog.New(slog.DiscardHandler), "1.X.X")
		require.NoError(t, errC)

		sampler, err := initSampler("test", c)
		require.NoError(t, err)
		assert.Equal(t, sdktrace.AlwaysSample(), sampler)
	})

	t.Run("OTEL_TRACES_SAMPLER set to non-jaeger_remote", func(t *testing.T) {
		t.Setenv("OTEL_TRACES_SAMPLER", "always_on")

		f := pflag.NewFlagSet("config", pflag.ContinueOnError)
		c, errC := config.New(f, slog.New(slog.DiscardHandler), "1.X.X")
		require.NoError(t, errC)

		sampler, err := initSampler("test", c)
		require.NoError(t, err)
		assert.Nil(t, sampler)
	})

	t.Run("OTEL_TRACES_SAMPLER set to jaeger_remote", func(t *testing.T) {
		t.Setenv("OTEL_TRACES_SAMPLER", "jaeger_remote")

		f := pflag.NewFlagSet("config", pflag.ContinueOnError)
		c, errC := config.New(f, slog.New(slog.DiscardHandler), "1.X.X")
		require.NoError(t, errC)

		sampler, err := initSampler("test", c)
		require.NoError(t, err)

		// not really any way to assert on the sampler other than calling
		// Description()...
		assert.Equal(t, "JaegerRemoteSampler{}", sampler.Description())
	})
}

// T.Setenv: the OpenTelemetry and Jaeger SDKs (third-party code) read OTEL_*/JAEGER_* from the process environment.
//
//nolint:paralleltest // see the comment above
func TestJaegerRemoteSamplerOpts(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		f := pflag.NewFlagSet("config", pflag.ContinueOnError)
		c, errC := config.New(f, slog.New(slog.DiscardHandler), "1.X.X")
		require.NoError(t, errC)

		url, refreshInterval, maxOperations, err := jaegerRemoteSamplerOpts(c)
		require.NoError(t, err)
		assert.Equal(t, defaultSamplerURL, url)
		assert.Equal(t, defaultSamplingRefreshInterval, refreshInterval)
		assert.Equal(t, defaultSamplingMaxOperations, maxOperations)
	})

	t.Run("JAEGER_SAMPLER_MANAGER_HOST_PORT set", func(t *testing.T) {
		expected := "http://example.com:1234"
		t.Setenv("JAEGER_SAMPLER_MANAGER_HOST_PORT", expected)

		f := pflag.NewFlagSet("config", pflag.ContinueOnError)
		c, errC := config.New(f, slog.New(slog.DiscardHandler), "1.X.X")
		require.NoError(t, errC)

		url, _, _, err := jaegerRemoteSamplerOpts(c)
		require.NoError(t, err)
		assert.Equal(t, expected, url)
	})

	t.Run("JAEGER_SAMPLER_REFRESH_INTERVAL set", func(t *testing.T) {
		expected := 42 * time.Second
		t.Setenv("JAEGER_SAMPLER_REFRESH_INTERVAL", expected.String())

		f := pflag.NewFlagSet("config", pflag.ContinueOnError)
		c, errC := config.New(f, slog.New(slog.DiscardHandler), "1.X.X")
		require.NoError(t, errC)

		_, refreshInterval, _, err := jaegerRemoteSamplerOpts(c)
		require.NoError(t, err)
		assert.Equal(t, expected, refreshInterval)
	})

	t.Run("JAEGER_SAMPLER_MAX_OPERATIONS set", func(t *testing.T) {
		expected := 42
		t.Setenv("JAEGER_SAMPLER_MAX_OPERATIONS", strconv.Itoa(expected))

		f := pflag.NewFlagSet("config", pflag.ContinueOnError)
		c, errC := config.New(f, slog.New(slog.DiscardHandler), "1.X.X")
		require.NoError(t, errC)

		_, _, maxOperations, err := jaegerRemoteSamplerOpts(c)
		require.NoError(t, err)
		assert.Equal(t, expected, maxOperations)
	})

	t.Run("invalid JAEGER_SAMPLER_REFRESH_INTERVAL", func(t *testing.T) {
		t.Setenv("JAEGER_SAMPLER_REFRESH_INTERVAL", "bogus")

		f := pflag.NewFlagSet("config", pflag.ContinueOnError)
		c, errC := config.New(f, slog.New(slog.DiscardHandler), "1.X.X")
		require.NoError(t, errC)

		_, _, _, err := jaegerRemoteSamplerOpts(c)
		require.EqualError(t, err, `parsing JAEGER_SAMPLER_REFRESH_INTERVAL: time: invalid duration "bogus"`)
	})

	t.Run("invalid JAEGER_SAMPLER_MAX_OPERATIONS", func(t *testing.T) {
		t.Setenv("JAEGER_SAMPLER_MAX_OPERATIONS", "bogus")

		f := pflag.NewFlagSet("config", pflag.ContinueOnError)
		_, errC := config.New(f, slog.New(slog.DiscardHandler), "1.X.X")
		require.ErrorContains(t, errC,
			"'jaeger.sampler.max.operations' cannot parse value as 'int': strconv.ParseInt: invalid syntax")
	})
}

func TestInitResource(t *testing.T) {
	t.Parallel()

	t.Run("defaults, no env", func(t *testing.T) {
		t.Parallel()

		res, err := initResource(context.Background(), "test", "1.2.3", nil)
		require.NoError(t, err)

		rmap := map[string]attribute.Value{}
		for _, attr := range res.Attributes() {
			rmap[string(attr.Key)] = attr.Value
		}

		// just spot-check a few things
		assert.Equal(t, "test", rmap["service.name"].AsString())
		assert.Equal(t, "1.2.3", rmap["service.version"].AsString())
		assert.Equal(t, "go", rmap["process.runtime.name"].AsString())
	})

	t.Run("with config map set", func(t *testing.T) {
		t.Parallel()

		attribs := map[string]string{
			"key1": "val1",
			"key2": "val2",
		}

		res, err := initResource(context.Background(), "test", "1.2.3", attribs)
		require.NoError(t, err)

		rmap := map[string]attribute.Value{}
		for _, attr := range res.Attributes() {
			rmap[string(attr.Key)] = attr.Value
		}

		assert.Equal(t, "val1", rmap["key1"].AsString())
		assert.Equal(t, "val2", rmap["key2"].AsString())
	})
}

// T.Setenv: the OpenTelemetry SDK (third-party code) reads OTEL_* from the process environment.
//
//nolint:paralleltest // see the comment above
func TestInit(t *testing.T) {
	t.Run("no config", func(t *testing.T) {
		svc := NewOtelService()
		err := svc.Init(context.Background(), &config.Config{})
		require.NoError(t, err)

		defer func() { _ = svc.Stop(context.Background()) }()

		assert.Nil(t, svc.otelTraceProvider)
	})

	t.Run("disabled", func(t *testing.T) {
		t.Setenv("OTEL_SDK_DISABLED", "true")
		t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "https://example.com:4318")

		f := pflag.NewFlagSet("config", pflag.ContinueOnError)
		c, errC := config.New(f, slog.New(slog.DiscardHandler), "1.X.X")
		require.NoError(t, errC)

		svc := NewOtelService()
		err := svc.Init(context.Background(), c)
		require.NoError(t, err)

		defer func() { _ = svc.Stop(context.Background()) }()

		assert.Nil(t, svc.otelTraceProvider)
	})

	t.Run("an endpoint creates the tracer provider without installing it", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "https://example.com:4318")

		before := otel.GetTracerProvider()
		svc := NewOtelService()
		err := svc.Init(context.Background(), &config.Config{
			OpenTelemetryOtlpEndpoint: "https://example.com:4318",
		})
		require.NoError(t, err)

		defer func() { _ = svc.Stop(context.Background()) }()

		assert.NotNil(t, svc.otelTraceProvider)
		assert.Equal(t, before, otel.GetTracerProvider(), "Init must not change the global provider")
	})
}

func TestNewErrorHandler(t *testing.T) {
	t.Parallel()

	tests := []struct {
		err         error
		wantContext map[string]any
		name        string
		wantMessage string
		wantLevel   slog.Level
	}{
		{
			name:        "the error is logged",
			err:         errTestError,
			wantLevel:   slog.LevelError,
			wantMessage: "OTel error",
			wantContext: map[string]any{"error": errTestError.Error()},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			obs, logs := slogutil.NewObserver(slog.LevelInfo)
			newErrorHandler(obs).Handle(tt.err)

			got := logs.All()
			require.Len(t, got, 1)
			assert.Equal(t, tt.wantLevel, got[0].Level)
			assert.Equal(t, tt.wantMessage, got[0].Message)
			assert.Equal(t, tt.wantContext, got[0].ContextMap())
		})
	}
}

func TestNewPropagator(t *testing.T) {
	t.Parallel()

	assert.ElementsMatch(t, []string{"traceparent", "tracestate", "baggage"}, newPropagator().Fields())
}

// Install registers the provider in the process-wide OpenTelemetry registry, which every test of the package that
// starts a span reads.
func TestInstall(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "https://example.com:4318")

	previous := otel.GetTracerProvider()

	t.Cleanup(func() { otel.SetTracerProvider(previous) })

	svc := NewOtelService()

	require.NoError(
		t,
		svc.Init(context.Background(), &config.Config{OpenTelemetryOtlpEndpoint: "https://example.com:4318"}),
	)
	defer func() { _ = svc.Stop(context.Background()) }()

	svc.Install(slog.New(slog.DiscardHandler))

	assert.Same(t, svc.otelTraceProvider, otel.GetTracerProvider())

	// a service that found OpenTelemetry disabled installs a no-op provider
	disabled := NewOtelService()
	disabled.Install(slog.New(slog.DiscardHandler))
	assert.Equal(t, noop.NewTracerProvider(), otel.GetTracerProvider())
}

// ExportEnvironment and t.Setenv change the process environment, which the OpenTelemetry SDK reads.
func TestExportEnvironment(t *testing.T) {
	tests := []struct {
		name         string
		endpoint     string
		otlpEndpoint string
		wantEnv      string
		wantEndpoint string
	}{
		{
			name:         "the openTelemetryOtlpEndpoint element is exported",
			endpoint:     "https://example.com:4318",
			wantEnv:      "https://example.com:4318",
			wantEndpoint: "https://example.com:4318",
		},
		{
			name:         "the OTLP exporter endpoint is exported when the environment has none",
			otlpEndpoint: "https://otlp.example.com:4318",
			wantEnv:      "https://otlp.example.com:4318",
			wantEndpoint: "https://otlp.example.com:4318",
		},
		{name: "no endpoint exports nothing", endpoint: "", wantEnv: "", wantEndpoint: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")

			conf := &config.Config{OpenTelemetryOtlpEndpoint: tt.endpoint}
			conf.OtelConfig.Exporter.Otlp.Endpoint = tt.otlpEndpoint
			ExportEnvironment(conf)
			assert.Equal(t, tt.wantEnv, os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
			assert.Equal(t, tt.wantEndpoint, conf.OtelConfig.Exporter.Otlp.Endpoint)
		})
	}
}

func TestExportEnvironment_EnvironmentVariableTakesPrecedence(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "https://example.com:4318")

	f := pflag.NewFlagSet("config", pflag.ContinueOnError)
	c, errC := config.New(f, slog.New(slog.DiscardHandler), "1.X.X")
	require.NoError(t, errC)

	c.OpenTelemetryOtlpEndpoint = "https://bogus.com:4317"

	ExportEnvironment(c)

	assert.Equal(t, "https://example.com:4318", os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
}

// shutdownProbe records the shutdown of a part of the service and fails it on request.
type shutdownProbe struct {
	err  error
	done bool
	mu   sync.Mutex
}

func (p *shutdownProbe) shutdown() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.done = true

	return p.err
}

func (p *shutdownProbe) wasShutDown() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.done
}

// probeExporter is a span exporter whose shutdown is observed.
type probeExporter struct{ probe *shutdownProbe }

func (probeExporter) ExportSpans(context.Context, []sdktrace.ReadOnlySpan) error { return nil }

func (e probeExporter) Shutdown(context.Context) error { return e.probe.shutdown() }

// probeProcessor is a span processor whose shutdown (the one of the tracer provider) is observed.
type probeProcessor struct{ probe *shutdownProbe }

func (probeProcessor) OnStart(context.Context, sdktrace.ReadWriteSpan) {}

func (probeProcessor) OnEnd(sdktrace.ReadOnlySpan) {}

func (p probeProcessor) Shutdown(context.Context) error { return p.probe.shutdown() }

func (probeProcessor) ForceFlush(context.Context) error { return nil }

// memoryMetricExporter keeps the metrics it receives, and its shutdown is observed.
type memoryMetricExporter struct {
	probe    *shutdownProbe
	exported []string
	mu       sync.Mutex
}

func (*memoryMetricExporter) Temporality(sdkmetric.InstrumentKind) metricdata.Temporality {
	return metricdata.CumulativeTemporality
}

func (*memoryMetricExporter) Aggregation(kind sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.DefaultAggregationSelector(kind)
}

func (e *memoryMetricExporter) Export(_ context.Context, data *metricdata.ResourceMetrics) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	for _, scope := range data.ScopeMetrics {
		for _, m := range scope.Metrics {
			e.exported = append(e.exported, m.Name)
		}
	}

	return nil
}

func (*memoryMetricExporter) ForceFlush(context.Context) error { return nil }

func (e *memoryMetricExporter) Shutdown(context.Context) error { return e.probe.shutdown() }

func (e *memoryMetricExporter) names() []string {
	e.mu.Lock()
	defer e.mu.Unlock()

	return append([]string(nil), e.exported...)
}

var (
	errExporterShutdown = errors.New("exporter shutdown failed")
	errTracerShutdown   = errors.New("tracer provider shutdown failed")
	errMeterShutdown    = errors.New("meter provider shutdown failed")
)

// TestStop checks that the failure of any part of the service does not keep the others from being
// shut down, that the errors that are returned are exactly the failing ones, and that the metrics that were pending
// are delivered to the exporter during the shutdown (the final flush).
func TestStop(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		exporterErr   error
		tracerErr     error
		meterErr      error
		wantErrs      []error
		wantNotErrors []error
	}{
		{name: "exporter fails", exporterErr: errExporterShutdown, wantErrs: []error{errExporterShutdown},
			wantNotErrors: []error{errTracerShutdown, errMeterShutdown}},
		{name: "tracer fails", tracerErr: errTracerShutdown, wantErrs: []error{errTracerShutdown},
			wantNotErrors: []error{errExporterShutdown, errMeterShutdown}},
		{name: "meter fails", meterErr: errMeterShutdown, wantErrs: []error{errMeterShutdown},
			wantNotErrors: []error{errExporterShutdown, errTracerShutdown}},
		{name: "all fail", exporterErr: errExporterShutdown, tracerErr: errTracerShutdown,
			meterErr: errMeterShutdown, wantErrs: []error{errExporterShutdown, errTracerShutdown, errMeterShutdown}},
		{name: "none fails", wantNotErrors: []error{errExporterShutdown, errTracerShutdown, errMeterShutdown}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			exporterProbe := &shutdownProbe{err: tt.exporterErr}
			tracerProbe := &shutdownProbe{err: tt.tracerErr}
			metrics := &memoryMetricExporter{probe: &shutdownProbe{err: tt.meterErr}}
			// the periodic reader exports on its interval, and when it is shut down: nothing is exported before.
			reader := sdkmetric.NewPeriodicReader(metrics, sdkmetric.WithInterval(time.Hour))

			svc := NewOtelService(WithMetricReader(reader))
			svc.otelExporter = probeExporter{probe: exporterProbe}
			svc.otelTraceProvider = sdktrace.NewTracerProvider(
				sdktrace.WithSpanProcessor(probeProcessor{probe: tracerProbe}),
			)
			require.NoError(
				t,
				svc.InitMetrics(t.Context(), &config.Config{OpenTelemetryOtlpEndpoint: "http://localhost:4318"}),
			)

			counter, err := svc.MeterProvider().Meter("test").Int64Counter("test.pending")
			require.NoError(t, err)
			counter.Add(t.Context(), 1)
			require.Empty(t, metrics.names(), "nothing was exported before the shutdown")

			stopErr := svc.Stop(t.Context())

			assert.True(t, exporterProbe.wasShutDown(), "the span exporter was shut down")
			assert.True(t, tracerProbe.wasShutDown(), "the tracer provider was shut down")
			assert.True(t, metrics.probe.wasShutDown(), "the meter provider (its reader and exporter) was shut down")
			assert.Contains(t, metrics.names(), "test.pending", "the pending metrics were flushed by the shutdown")

			for _, want := range tt.wantErrs {
				require.ErrorIs(t, stopErr, want)
			}

			for _, notWant := range tt.wantNotErrors {
				require.NotErrorIs(t, stopErr, notWant)
			}

			if len(tt.wantErrs) == 0 {
				require.NoError(t, stopErr)
			}
		})
	}
}

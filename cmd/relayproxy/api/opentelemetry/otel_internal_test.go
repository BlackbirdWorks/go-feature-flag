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
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
	"github.com/blackbirdworks/semaphore/testhelpers/slogutil"
)

const (
	jaegerRemote             = "jaeger_remote"
	httpsExampleCom4318      = "https://example.com:4318"
	httpsOtlpExampleCom      = "https://otlp.example.com:4318"
	otelExporterOtlpEndpoint = "OTEL_EXPORTER_OTLP_ENDPOINT"
)

var (
	errTestError = errors.New("test error")
)

//nolint:paralleltest // uses t.Setenv because OpenTelemetry and Jaeger SDKs read OTEL_*/JAEGER_* env vars
func TestInitSampler(t *testing.T) {
	t.Run("OTEL_TRACES_SAMPLER unset", func(t *testing.T) {
		f := pflag.NewFlagSet(fixtures.ConfigFlagName, pflag.ContinueOnError)
		c, errC := config.New(f, slog.New(slog.DiscardHandler), fixtures.VersionWildcard)
		require.NoError(t, errC)

		got, err := initSampler(fixtures.PlaceholderTest, settingsOf(c))
		require.NoError(t, err)
		assert.Equal(t, sdktrace.AlwaysSample(), got.sampler)
	})

	t.Run("OTEL_TRACES_SAMPLER set to non-jaeger_remote", func(t *testing.T) {
		t.Setenv("OTEL_TRACES_SAMPLER", "always_on")

		f := pflag.NewFlagSet(fixtures.ConfigFlagName, pflag.ContinueOnError)
		c, errC := config.New(f, slog.New(slog.DiscardHandler), fixtures.VersionWildcard)
		require.NoError(t, errC)

		got, err := initSampler(fixtures.PlaceholderTest, settingsOf(c))
		require.NoError(t, err)
		assert.Nil(t, got.sampler)
	})

	t.Run("OTEL_TRACES_SAMPLER set to jaeger_remote", func(t *testing.T) {
		t.Setenv("OTEL_TRACES_SAMPLER", jaegerRemote)

		f := pflag.NewFlagSet(fixtures.ConfigFlagName, pflag.ContinueOnError)
		c, errC := config.New(f, slog.New(slog.DiscardHandler), fixtures.VersionWildcard)
		require.NoError(t, errC)

		got, err := initSampler(fixtures.PlaceholderTest, settingsOf(c))
		require.NoError(t, err)

		// not really any way to assert on the sampler other than calling
		// Description()...
		assert.Equal(t, "JaegerRemoteSampler{}", got.sampler.Description())
	})
}

//nolint:paralleltest // uses t.Setenv because OpenTelemetry and Jaeger SDKs read OTEL_*/JAEGER_* env vars
func TestJaegerRemoteSamplerOpts(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		f := pflag.NewFlagSet(fixtures.ConfigFlagName, pflag.ContinueOnError)
		c, errC := config.New(f, slog.New(slog.DiscardHandler), fixtures.VersionWildcard)
		require.NoError(t, errC)

		opts, err := jaegerRemoteSamplerOpts(settingsOf(c))
		require.NoError(t, err)
		assert.Equal(t, defaultSamplerURL, opts.url)
		assert.Equal(t, defaultSamplingRefreshInterval, opts.refreshInterval)
		assert.Equal(t, defaultSamplingMaxOperations, opts.maxOperations)
	})

	t.Run("JAEGER_SAMPLER_MANAGER_HOST_PORT set", func(t *testing.T) {
		expected := "http://example.com:1234"
		t.Setenv("JAEGER_SAMPLER_MANAGER_HOST_PORT", expected)

		f := pflag.NewFlagSet(fixtures.ConfigFlagName, pflag.ContinueOnError)
		c, errC := config.New(f, slog.New(slog.DiscardHandler), fixtures.VersionWildcard)
		require.NoError(t, errC)

		opts, err := jaegerRemoteSamplerOpts(settingsOf(c))
		require.NoError(t, err)
		assert.Equal(t, expected, opts.url)
	})

	t.Run("JAEGER_SAMPLER_REFRESH_INTERVAL set", func(t *testing.T) {
		expected := 42 * time.Second
		t.Setenv("JAEGER_SAMPLER_REFRESH_INTERVAL", expected.String())

		f := pflag.NewFlagSet(fixtures.ConfigFlagName, pflag.ContinueOnError)
		c, errC := config.New(f, slog.New(slog.DiscardHandler), fixtures.VersionWildcard)
		require.NoError(t, errC)

		opts, err := jaegerRemoteSamplerOpts(settingsOf(c))
		require.NoError(t, err)
		assert.Equal(t, expected, opts.refreshInterval)
	})

	t.Run("JAEGER_SAMPLER_MAX_OPERATIONS set", func(t *testing.T) {
		expected := 42
		t.Setenv("JAEGER_SAMPLER_MAX_OPERATIONS", strconv.Itoa(expected))

		f := pflag.NewFlagSet(fixtures.ConfigFlagName, pflag.ContinueOnError)
		c, errC := config.New(f, slog.New(slog.DiscardHandler), fixtures.VersionWildcard)
		require.NoError(t, errC)

		opts, err := jaegerRemoteSamplerOpts(settingsOf(c))
		require.NoError(t, err)
		assert.Equal(t, expected, opts.maxOperations)
	})

	t.Run("invalid JAEGER_SAMPLER_REFRESH_INTERVAL", func(t *testing.T) {
		t.Setenv("JAEGER_SAMPLER_REFRESH_INTERVAL", "bogus")

		f := pflag.NewFlagSet(fixtures.ConfigFlagName, pflag.ContinueOnError)
		c, errC := config.New(f, slog.New(slog.DiscardHandler), fixtures.VersionWildcard)
		require.NoError(t, errC)

		_, err := jaegerRemoteSamplerOpts(settingsOf(c))
		require.EqualError(t, err, `parsing JAEGER_SAMPLER_REFRESH_INTERVAL: time: invalid duration "bogus"`)
	})

	t.Run("invalid JAEGER_SAMPLER_MAX_OPERATIONS", func(t *testing.T) {
		t.Setenv("JAEGER_SAMPLER_MAX_OPERATIONS", "bogus")

		f := pflag.NewFlagSet(fixtures.ConfigFlagName, pflag.ContinueOnError)
		_, errC := config.New(f, slog.New(slog.DiscardHandler), fixtures.VersionWildcard)
		require.ErrorContains(t, errC,
			"'jaeger.sampler.max.operations' cannot parse value as 'int': strconv.ParseInt: invalid syntax")
	})
}

func TestInitResource(t *testing.T) {
	t.Parallel()

	t.Run("defaults, no env", func(t *testing.T) {
		t.Parallel()

		res, err := initResource(context.Background(), fixtures.PlaceholderTest, fixtures.VersionOneTwoThree, nil)
		require.NoError(t, err)

		rmap := map[string]attribute.Value{}
		for _, attr := range res.Attributes() {
			rmap[string(attr.Key)] = attr.Value
		}

		// just spot-check a few things
		assert.Equal(t, fixtures.PlaceholderTest, rmap["service.name"].AsString())
		assert.Equal(t, fixtures.VersionOneTwoThree, rmap["service.version"].AsString())
		assert.Equal(t, "go", rmap["process.runtime.name"].AsString())
	})

	t.Run("with config map set", func(t *testing.T) {
		t.Parallel()

		attribs := map[string]string{
			"key1": "val1",
			"key2": "val2",
		}

		res, err := initResource(context.Background(), fixtures.PlaceholderTest, fixtures.VersionOneTwoThree, attribs)
		require.NoError(t, err)

		rmap := map[string]attribute.Value{}
		for _, attr := range res.Attributes() {
			rmap[string(attr.Key)] = attr.Value
		}

		assert.Equal(t, "val1", rmap["key1"].AsString())
		assert.Equal(t, "val2", rmap["key2"].AsString())
	})
}

//nolint:paralleltest // uses t.Setenv because OpenTelemetry SDK reads OTEL_* from the process environment
func TestInit(t *testing.T) {
	t.Run("no config", func(t *testing.T) {
		svc := NewOtelService()
		err := svc.Init(context.Background(), settingsOf(&config.Config{}))
		require.NoError(t, err)

		defer func() { _ = svc.Stop(context.Background()) }()

		assert.Nil(t, svc.otelTraceProvider)
	})

	t.Run("disabled", func(t *testing.T) {
		t.Setenv("OTEL_SDK_DISABLED", "true")
		t.Setenv(otelExporterOtlpEndpoint, httpsExampleCom4318)

		f := pflag.NewFlagSet(fixtures.ConfigFlagName, pflag.ContinueOnError)
		c, errC := config.New(f, slog.New(slog.DiscardHandler), fixtures.VersionWildcard)
		require.NoError(t, errC)

		svc := NewOtelService()
		err := svc.Init(context.Background(), settingsOf(c))
		require.NoError(t, err)

		defer func() { _ = svc.Stop(context.Background()) }()

		assert.Nil(t, svc.otelTraceProvider)
	})

	t.Run("an endpoint creates the tracer provider without installing it", func(t *testing.T) {
		t.Setenv(otelExporterOtlpEndpoint, httpsExampleCom4318)

		before := otel.GetTracerProvider()
		svc := NewOtelService()
		err := svc.Init(context.Background(), settingsOf(&config.Config{
			OpenTelemetryOtlpEndpoint: httpsExampleCom4318,
		}))
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
			newErrorHandler(t.Context(), obs).Handle(tt.err)

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
	t.Setenv(otelExporterOtlpEndpoint, httpsExampleCom4318)

	previous := otel.GetTracerProvider()

	t.Cleanup(func() { otel.SetTracerProvider(previous) })

	svc := NewOtelService()

	require.NoError(
		t,
		svc.Init(context.Background(), settingsOf(&config.Config{OpenTelemetryOtlpEndpoint: httpsExampleCom4318})),
	)
	defer func() { _ = svc.Stop(context.Background()) }()

	svc.Install(t.Context(), slog.New(slog.DiscardHandler))

	assert.Same(t, svc.otelTraceProvider, otel.GetTracerProvider())

	// a service that found OpenTelemetry disabled installs a no-op provider
	disabled := NewOtelService()
	disabled.Install(t.Context(), slog.New(slog.DiscardHandler))
	assert.Equal(t, noop.NewTracerProvider(), otel.GetTracerProvider())
}

const otelTracesSampler = "OTEL_TRACES_SAMPLER"

// ExportEnvironment and t.Setenv change the process environment, which the OpenTelemetry SDK reads.
func TestExportEnvironment(t *testing.T) {
	tests := []struct {
		name         string
		endpoint     string
		otlpEndpoint string
		wantEnv      string
	}{
		{
			name:     "the openTelemetryOtlpEndpoint element is exported",
			endpoint: httpsExampleCom4318,
			wantEnv:  httpsExampleCom4318,
		},
		{
			name:         "the OTLP exporter endpoint is exported when the environment has none",
			otlpEndpoint: httpsOtlpExampleCom,
			wantEnv:      httpsOtlpExampleCom,
		},
		{name: "no endpoint exports nothing", endpoint: "", wantEnv: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(otelExporterOtlpEndpoint, "")

			conf := &config.Config{OpenTelemetryOtlpEndpoint: tt.endpoint}
			conf.OtelConfig.Exporter.Otlp.Endpoint = tt.otlpEndpoint
			ExportEnvironment(settingsOf(conf))
			assert.Equal(t, tt.wantEnv, os.Getenv(otelExporterOtlpEndpoint))
		})
	}
}

// The sampler of the configuration reaches the SDK, which reads OTEL_TRACES_SAMPLER; the variable of the operator wins,
// and the jaeger_remote sampler is built by the service itself.
func TestExportEnvironment_Sampler(t *testing.T) {
	tests := []struct {
		name    string
		sampler string
		current string
		wantEnv string
	}{
		{name: "the sampler of the file is exported", sampler: "always_off", wantEnv: "always_off"},
		{name: "a ratio sampler is exported", sampler: "parentbased_traceidratio", wantEnv: "parentbased_traceidratio"},
		{name: "the variable of the operator wins", sampler: "always_off", current: "always_on", wantEnv: "always_on"},
		{name: "jaeger_remote is not exported", sampler: "jaeger_remote", wantEnv: ""},
		{name: "no sampler exports nothing", wantEnv: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(otelTracesSampler, tt.current)
			t.Setenv(otelExporterOtlpEndpoint, "")

			ExportEnvironment(&Settings{OTLPEndpoint: httpsExampleCom4318, Sampler: tt.sampler})
			assert.Equal(t, tt.wantEnv, os.Getenv(otelTracesSampler))
		})
	}
}

func TestExportEnvironment_EnvironmentVariableTakesPrecedence(t *testing.T) {
	t.Setenv(otelExporterOtlpEndpoint, httpsExampleCom4318)

	f := pflag.NewFlagSet(fixtures.ConfigFlagName, pflag.ContinueOnError)
	c, errC := config.New(f, slog.New(slog.DiscardHandler), fixtures.VersionWildcard)
	require.NoError(t, errC)

	c.OpenTelemetryOtlpEndpoint = "https://bogus.com:4317"

	ExportEnvironment(settingsOf(c))

	assert.Equal(t, httpsExampleCom4318, os.Getenv(otelExporterOtlpEndpoint))
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

// probeMetricReader is a manual metric reader whose shutdown is observed and collects pending metrics on shutdown.
type probeMetricReader struct {
	*sdkmetric.ManualReader

	probe *shutdownProbe
	rm    metricdata.ResourceMetrics
	mu    sync.Mutex
}

func (r *probeMetricReader) Shutdown(ctx context.Context) error {
	r.mu.Lock()
	collectErr := r.Collect(ctx, &r.rm)
	r.mu.Unlock()

	readerErr := r.ManualReader.Shutdown(ctx)
	probeErr := r.probe.shutdown()

	return errors.Join(collectErr, readerErr, probeErr)
}

func (r *probeMetricReader) names() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	var out []string

	for _, scope := range r.rm.ScopeMetrics {
		for _, item := range scope.Metrics {
			out = append(out, item.Name)
		}
	}

	return out
}

var (
	errExporterShutdown = errors.New("exporter shutdown failed")
	errTracerShutdown   = errors.New("tracer provider shutdown failed")
	errMeterShutdown    = errors.New("meter provider shutdown failed")
)

// TestStop checks that the failure of any part of the service does not keep the others from being
// shut down, that the errors that are returned are exactly the failing ones, and that the metrics that were pending
// are delivered to the reader during the shutdown (the final flush).
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
			meterProbe := &shutdownProbe{err: tt.meterErr}
			reader := &probeMetricReader{
				ManualReader: sdkmetric.NewManualReader(),
				probe:        meterProbe,
			}

			svc := NewOtelService(WithMetricReader(reader))
			svc.otelExporter = probeExporter{probe: exporterProbe}
			svc.otelTraceProvider = sdktrace.NewTracerProvider(
				sdktrace.WithSpanProcessor(probeProcessor{probe: tracerProbe}),
			)
			require.NoError(
				t,
				svc.InitMetrics(
					t.Context(),
					settingsOf(&config.Config{OpenTelemetryOtlpEndpoint: "http://localhost:4318"}),
				),
			)

			counter, err := svc.MeterProvider().Meter(fixtures.PlaceholderTest).Int64Counter("test.pending")
			require.NoError(t, err)
			counter.Add(t.Context(), 1)
			require.Empty(t, reader.names(), "nothing was collected before the shutdown")

			stopErr := svc.Stop(t.Context())

			assert.True(t, exporterProbe.wasShutDown(), "the span exporter was shut down")
			assert.True(t, tracerProbe.wasShutDown(), "the tracer provider was shut down")
			assert.True(t, meterProbe.wasShutDown(), "the meter provider (its reader) was shut down")
			assert.Contains(t, reader.names(), "test.pending", "the pending metrics were flushed by the shutdown")

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

// settingsOf is the telemetry settings of a configuration. The apiwire package does it for the relay proxy, but it
// imports this package, so the internal tests can not use it.
func settingsOf(conf *config.Config) *Settings {
	return &Settings{
		ResourceAttributes:           conf.OtelConfig.Resource.Attributes,
		Version:                      conf.Version,
		OTLPEndpoint:                 conf.OpenTelemetryOtlpEndpoint,
		ExporterOTLPEndpoint:         conf.OtelConfig.Exporter.Otlp.Endpoint,
		ServiceName:                  conf.OtelConfig.Service.Name,
		Sampler:                      conf.OtelConfig.Traces.Sampler,
		JaegerSamplerManagerHostPort: conf.JaegerConfig.Sampler.Manager.Host.Port,
		JaegerSamplerRefreshInterval: conf.JaegerConfig.Sampler.Refresh.Interval,
		JaegerSamplerMaxOperations:   conf.JaegerConfig.Sampler.Max.Operations,
		SDKDisabled:                  conf.OtelConfig.SDK.Disabled,
	}
}

func TestEnabled(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		settings Settings
		want     bool
	}{
		{name: "no endpoint", settings: Settings{}, want: false},
		{name: "the legacy endpoint element", settings: Settings{OTLPEndpoint: httpsExampleCom4318}, want: true},
		{name: "the OTLP exporter endpoint", settings: Settings{ExporterOTLPEndpoint: httpsOtlpExampleCom}, want: true},
		{
			name:     "disabled by the SDK setting",
			settings: Settings{OTLPEndpoint: httpsExampleCom4318, SDKDisabled: true},
			want:     false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, Enabled(&tt.settings))
		})
	}
}

// initEnvironment is every variable that Init and the SDK behind it read from the process environment, with the
// value that the error tests start from: a variable of the machine that runs the tests can not change the outcome.
func initEnvironment() map[string]string {
	return map[string]string{
		"OTEL_SDK_DISABLED":                     "false",
		"OTEL_TRACES_EXPORTER":                  "otlp",
		"OTEL_TRACES_SAMPLER":                   "",
		"OTEL_TRACES_SAMPLER_ARG":               "",
		"OTEL_EXPORTER_OTLP_ENDPOINT":           httpsExampleCom4318,
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT":    "",
		"OTEL_EXPORTER_OTLP_PROTOCOL":           "http/protobuf",
		"OTEL_EXPORTER_OTLP_TRACES_PROTOCOL":    "",
		"OTEL_RESOURCE_ATTRIBUTES":              "",
		"OTEL_SERVICE_NAME":                     "",
		"JAEGER_SAMPLER_MANAGER_HOST_PORT":      "",
		"JAEGER_SAMPLER_REFRESH_INTERVAL":       "",
		"JAEGER_SAMPLER_MAX_OPERATIONS":         "",
		"OTEL_EXPORTER_OTLP_HEADERS":            "",
		"OTEL_EXPORTER_OTLP_TRACES_HEADERS":     "",
		"OTEL_EXPORTER_OTLP_CERTIFICATE":        "",
		"OTEL_EXPORTER_OTLP_TRACES_CERTIFICATE": "",
		"OTEL_EXPORTER_OTLP_COMPRESSION":        "",
		"OTEL_EXPORTER_OTLP_TRACES_COMPRESSION": "",
		"OTEL_EXPORTER_OTLP_TIMEOUT":            "",
		"OTEL_EXPORTER_OTLP_TRACES_TIMEOUT":     "",
		"OTEL_EXPORTER_OTLP_INSECURE":           "",
		"OTEL_EXPORTER_OTLP_TRACES_INSECURE":    "",
	}
}

// TestInit_Errors checks that a configuration that can not be turned into providers is reported with the step that
// failed, and that nothing is created. t.Setenv: the OpenTelemetry SDK reads OTEL_* from the process environment,
// every case sets all of them (initEnvironment) and overrides the one it is about.
func TestInit_Errors(t *testing.T) {
	tests := []struct {
		env            map[string]string
		name           string
		wantErr        string
		wantNotInError string
		settings       Settings
	}{
		{
			name:           "an unknown span exporter",
			env:            map[string]string{"OTEL_TRACES_EXPORTER": "unknown-exporter"},
			settings:       Settings{OTLPEndpoint: httpsExampleCom4318},
			wantErr:        "initializing OTel exporter",
			wantNotInError: "sampler",
		},
		{
			name: "a refresh interval of the jaeger sampler that is not a duration",
			settings: Settings{
				OTLPEndpoint: httpsExampleCom4318, Sampler: jaegerRemote, JaegerSamplerRefreshInterval: "soon",
			},
			wantErr:        "initializing OTel sampler: parsing JAEGER_SAMPLER_REFRESH_INTERVAL",
			wantNotInError: "exporter",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range initEnvironment() {
				t.Setenv(k, v)
			}

			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			svc := NewOtelService()
			err := svc.Init(t.Context(), &tt.settings)

			require.Error(t, err)
			require.ErrorContains(t, err, tt.wantErr)
			assert.NotContains(t, err.Error(), tt.wantNotInError, "the error is the one of the step under test")
			assert.Nil(t, svc.otelTraceProvider)
		})
	}
}

func TestInitMetrics_UnknownExporter(t *testing.T) {
	for k, v := range initEnvironment() {
		t.Setenv(k, v)
	}

	t.Setenv("OTEL_METRICS_EXPORTER", "unknown-exporter")

	svc := NewOtelService()
	err := svc.InitMetrics(t.Context(), &Settings{OTLPEndpoint: httpsExampleCom4318})

	require.ErrorContains(t, err, "initializing OTel metric exporter")
	assert.Nil(t, svc.MeterProvider())
}

// TestSettingsOf pins the mapping of settingsOf with the fixture of the apiwire test of the production mapping
// (TestTelemetry): the two mappings can not be compared in one package, the apiwire package imports this one, so
// both are compared with the same expected settings and drift fails one of the tests.
func TestSettingsOf(t *testing.T) {
	t.Parallel()

	conf := &config.Config{Version: "v3", OpenTelemetryOtlpEndpoint: "http://legacy:4318"}
	conf.OtelConfig.Exporter.Otlp.Endpoint = "http://exporter:4318"
	conf.OtelConfig.Service.Name = "svc"
	conf.OtelConfig.Traces.Sampler = jaegerRemote
	conf.OtelConfig.SDK.Disabled = true
	conf.OtelConfig.Resource.Attributes = map[string]string{"k": "v"}
	conf.JaegerConfig.Sampler.Manager.Host.Port = "host:5778"
	conf.JaegerConfig.Sampler.Refresh.Interval = "30s"
	conf.JaegerConfig.Sampler.Max.Operations = 7

	assert.Equal(t, &Settings{
		ResourceAttributes:           map[string]string{"k": "v"},
		Version:                      "v3",
		OTLPEndpoint:                 "http://legacy:4318",
		ExporterOTLPEndpoint:         "http://exporter:4318",
		ServiceName:                  "svc",
		Sampler:                      jaegerRemote,
		JaegerSamplerManagerHostPort: "host:5778",
		JaegerSamplerRefreshInterval: "30s",
		JaegerSamplerMaxOperations:   7,
		SDKDisabled:                  true,
	}, settingsOf(conf))
}

package opentelemetry

import (
	"context"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
)

//nolint:paralleltest,tparallel // t.Setenv: the OpenTelemetry and Jaeger SDKs (third-party code) read OTEL_*/JAEGER_* from the process environment
func TestInitSampler(t *testing.T) {
	t.Run("OTEL_TRACES_SAMPLER unset", func(t *testing.T) {
		f := pflag.NewFlagSet("config", pflag.ContinueOnError)
		c, errC := config.New(f, zap.NewNop(), "1.X.X")
		require.NoError(t, errC)

		sampler, err := initSampler("test", c)
		require.NoError(t, err)
		assert.Equal(t, sdktrace.AlwaysSample(), sampler)
	})

	t.Run("OTEL_TRACES_SAMPLER set to non-jaeger_remote", func(t *testing.T) {
		t.Setenv("OTEL_TRACES_SAMPLER", "always_on")
		f := pflag.NewFlagSet("config", pflag.ContinueOnError)
		c, errC := config.New(f, zap.NewNop(), "1.X.X")
		require.NoError(t, errC)

		sampler, err := initSampler("test", c)
		require.NoError(t, err)
		assert.Nil(t, sampler)
	})

	t.Run("OTEL_TRACES_SAMPLER set to jaeger_remote", func(t *testing.T) {
		t.Setenv("OTEL_TRACES_SAMPLER", "jaeger_remote")
		f := pflag.NewFlagSet("config", pflag.ContinueOnError)
		c, errC := config.New(f, zap.NewNop(), "1.X.X")
		require.NoError(t, errC)

		sampler, err := initSampler("test", c)
		require.NoError(t, err)

		// not really any way to assert on the sampler other than calling
		// Description()...
		assert.Equal(t, "JaegerRemoteSampler{}", sampler.Description())
	})
}

//nolint:paralleltest,tparallel // t.Setenv: the OpenTelemetry and Jaeger SDKs (third-party code) read OTEL_*/JAEGER_* from the process environment
func TestJaegerRemoteSamplerOpts(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		f := pflag.NewFlagSet("config", pflag.ContinueOnError)
		c, errC := config.New(f, zap.NewNop(), "1.X.X")
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
		c, errC := config.New(f, zap.NewNop(), "1.X.X")
		require.NoError(t, errC)

		url, _, _, err := jaegerRemoteSamplerOpts(c)
		require.NoError(t, err)
		assert.Equal(t, expected, url)
	})

	t.Run("JAEGER_SAMPLER_REFRESH_INTERVAL set", func(t *testing.T) {
		expected := 42 * time.Second
		t.Setenv("JAEGER_SAMPLER_REFRESH_INTERVAL", expected.String())

		f := pflag.NewFlagSet("config", pflag.ContinueOnError)
		c, errC := config.New(f, zap.NewNop(), "1.X.X")
		require.NoError(t, errC)

		_, refreshInterval, _, err := jaegerRemoteSamplerOpts(c)
		require.NoError(t, err)
		assert.Equal(t, expected, refreshInterval)
	})

	t.Run("JAEGER_SAMPLER_MAX_OPERATIONS set", func(t *testing.T) {
		expected := 42
		t.Setenv("JAEGER_SAMPLER_MAX_OPERATIONS", strconv.Itoa(expected))

		f := pflag.NewFlagSet("config", pflag.ContinueOnError)
		c, errC := config.New(f, zap.NewNop(), "1.X.X")
		require.NoError(t, errC)

		_, _, maxOperations, err := jaegerRemoteSamplerOpts(c)
		require.NoError(t, err)
		assert.Equal(t, expected, maxOperations)
	})

	t.Run("invalid JAEGER_SAMPLER_REFRESH_INTERVAL", func(t *testing.T) {
		t.Setenv("JAEGER_SAMPLER_REFRESH_INTERVAL", "bogus")

		f := pflag.NewFlagSet("config", pflag.ContinueOnError)
		c, errC := config.New(f, zap.NewNop(), "1.X.X")
		require.NoError(t, errC)

		_, _, _, err := jaegerRemoteSamplerOpts(c)
		require.Error(t, err)
	})

	t.Run("invalid JAEGER_SAMPLER_MAX_OPERATIONS", func(t *testing.T) {
		t.Setenv("JAEGER_SAMPLER_MAX_OPERATIONS", "bogus")

		f := pflag.NewFlagSet("config", pflag.ContinueOnError)
		_, errC := config.New(f, zap.NewNop(), "1.X.X")
		require.Error(t, errC)
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

//nolint:paralleltest,tparallel // t.Setenv: the OpenTelemetry SDK (third-party code) reads OTEL_* from the process environment
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
		c, errC := config.New(f, zap.NewNop(), "1.X.X")
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
		name    string
		err     error
		wantLog []observer.LoggedEntry
	}{
		{
			name: "the error is logged to zap",
			err:  errors.New("test error"),
			wantLog: []observer.LoggedEntry{{
				Entry:   zapcore.Entry{Level: zap.ErrorLevel, Message: "OTel error"},
				Context: []zapcore.Field{zap.Error(errors.New("test error"))},
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			obs, logs := observer.New(zap.InfoLevel)
			newErrorHandler(zap.New(obs)).Handle(tt.err)

			assert.Equal(t, tt.wantLog, logs.AllUntimed())
		})
	}
}

func TestNewPropagator(t *testing.T) {
	t.Parallel()

	assert.ElementsMatch(t, []string{"traceparent", "tracestate", "baggage"}, newPropagator().Fields())
}

//nolint:paralleltest,tparallel // Install registers the provider in the process-wide OpenTelemetry registry, which every test of the package that starts a span reads
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

	svc.Install(zap.NewNop())

	assert.Same(t, svc.otelTraceProvider, otel.GetTracerProvider())
}

//nolint:paralleltest,tparallel // ExportEnvironment and t.Setenv change the process environment, which the OpenTelemetry SDK reads
func TestExportEnvironment(t *testing.T) {
	tests := []struct {
		name         string
		endpoint     string
		wantEnv      string
		wantEndpoint string
	}{
		{
			name:         "the openTelemetryOtlpEndpoint element is exported",
			endpoint:     "https://example.com:4318",
			wantEnv:      "https://example.com:4318",
			wantEndpoint: "https://example.com:4318",
		},
		{name: "no endpoint exports nothing", endpoint: "", wantEnv: "", wantEndpoint: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
			conf := &config.Config{OpenTelemetryOtlpEndpoint: tt.endpoint}
			ExportEnvironment(conf)
			assert.Equal(t, tt.wantEnv, os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
			assert.Equal(t, tt.wantEndpoint, conf.OtelConfig.Exporter.Otlp.Endpoint)
		})
	}
}

//nolint:paralleltest,tparallel // t.Setenv changes the process environment, which the OpenTelemetry SDK reads
func TestExportEnvironment_EnvironmentVariableTakesPrecedence(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "https://example.com:4318")
	f := pflag.NewFlagSet("config", pflag.ContinueOnError)
	c, errC := config.New(f, zap.NewNop(), "1.X.X")
	require.NoError(t, errC)
	c.OpenTelemetryOtlpEndpoint = "https://bogus.com:4317"

	ExportEnvironment(c)

	assert.Equal(t, "https://example.com:4318", os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
}

package opentelemetry_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api/opentelemetry"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
)

// metricNames collects the metrics of the reader and returns their names.
func metricNames(t *testing.T, reader sdkmetric.Reader) []string {
	t.Helper()

	var data metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(t.Context(), &data))

	var names []string

	for _, scope := range data.ScopeMetrics {
		for _, m := range scope.Metrics {
			names = append(names, m.Name)
		}
	}

	return names
}

func TestInitMetrics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		conf         *config.Config
		name         string
		wantProvider bool
	}{
		{
			name:         "configured",
			conf:         &config.Config{OpenTelemetryOtlpEndpoint: "http://localhost:4318"},
			wantProvider: true,
		},
		{name: "not configured", conf: &config.Config{}},
		{
			name: "disabled by the SDK setting",
			conf: func() *config.Config {
				c := &config.Config{OpenTelemetryOtlpEndpoint: "http://localhost:4318"}
				c.OtelConfig.SDK.Disabled = true

				return c
			}(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reader := sdkmetric.NewManualReader()
			svc := opentelemetry.NewOtelService(opentelemetry.WithMetricReader(reader))
			require.NoError(t, svc.InitMetrics(t.Context(), tt.conf))

			if !tt.wantProvider {
				assert.Nil(t, svc.MeterProvider(), "nothing is created when OpenTelemetry is not configured")

				return
			}

			require.NotNil(t, svc.MeterProvider())
			// the Go runtime instruments (OpenTelemetry semantic conventions for Go runtime metrics)
			assert.Subset(
				t,
				metricNames(t, reader),
				[]string{"go.memory.used", "go.goroutine.count", "go.processor.limit"},
			)

			// the stop flushes and stops the provider: the reader is shut down
			stopCtx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()

			require.NoError(t, svc.Stop(stopCtx))

			var data metricdata.ResourceMetrics
			assert.ErrorIs(t, reader.Collect(t.Context(), &data), sdkmetric.ErrReaderShutdown)
		})
	}
}

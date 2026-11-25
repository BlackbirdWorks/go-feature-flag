package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
)

// Test_OTelMetrics checks that the HTTP server metrics of a request go to the meter provider of the service,
// with the route.
func Test_OTelMetrics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		path      string
		wantRoute string
	}{
		{name: "huma operation", path: health2, wantRoute: health2},
		{name: "raw handler", path: "/v1/feature/some-flag/eval", wantRoute: "/v1/feature/{flagKey}/eval"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reader := sdkmetric.NewManualReader()
			meters := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
			traces := sdktrace.NewTracerProvider()
			srv := api.New(
				testConfig(t),
				service.Services{MonitoringService: failingMonitoring()},
				newTestLogger(t).Slog,
				api.WithTracing(traces, propagation.TraceContext{}),
				api.WithMeterProvider(meters),
			)

			method := http.MethodGet
			if tt.path != health2 {
				method = http.MethodPost
			}

			srv.Handler().ServeHTTP(httptest.NewRecorder(),
				httptest.NewRequestWithContext(t.Context(), method, tt.path, nil))

			var data metricdata.ResourceMetrics
			require.NoError(t, reader.Collect(t.Context(), &data))

			var routes []string

			for _, scope := range data.ScopeMetrics {
				for _, m := range scope.Metrics {
					if m.Name != "http.server.request.duration" {
						continue
					}

					histogram, ok := m.Data.(metricdata.Histogram[float64])
					require.True(t, ok)

					for _, point := range histogram.DataPoints {
						if route, found := point.Attributes.Value("http.route"); found {
							routes = append(routes, route.AsString())
						}
					}
				}
			}

			assert.Equal(t, []string{tt.wantRoute}, routes)
		})
	}
}

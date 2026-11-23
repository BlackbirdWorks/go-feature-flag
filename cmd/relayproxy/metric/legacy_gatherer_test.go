package metric_test

import (
	"testing"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMetrics_Gatherer(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		exposeLegacy bool
	}{
		{name: "semaphore series only by default", exposeLegacy: false},
		{name: "legacy series next to the semaphore ones", exposeLegacy: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m, err := metric.NewMetrics()
			require.NoError(t, err)
			m.IncFlagEvaluation("my-flag")
			m.IncGetManifestCall()

			families, err := m.Gatherer(tt.exposeLegacy).Gather()
			require.NoError(t, err)
			names := map[string]float64{}
			for _, f := range families {
				var sum float64
				for _, metricValue := range f.GetMetric() {
					sum += metricValue.GetCounter().GetValue()
				}
				names[f.GetName()] = sum
			}

			assert.Equal(t, 1.0, names["semaphore_flag_evaluations_total"])
			assert.Equal(t, 1.0, names["semaphore_get_manifest_total"])
			assert.Contains(t, names, "semaphore_build_info")
			assert.Contains(t, names, "go_goroutines", "metrics of the Go runtime are never renamed")
			if tt.exposeLegacy {
				assert.Equal(t, 1.0, names["gofeatureflag_flag_evaluations_total"])
				assert.Equal(t, 1.0, names["gofeatureflag_get_manifest_total"])
				assert.Contains(t, names, "gofeatureflag_build_info")
				assert.NotContains(t, names, "gofeatureflag_go_goroutines")
			} else {
				assert.NotContains(t, names, "gofeatureflag_flag_evaluations_total")
				assert.NotContains(t, names, "gofeatureflag_build_info")
			}
		})
	}
}

package metric_test

import (
	"sync/atomic"
	"testing"

	prom "github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
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

			assert.InDelta(t, 1.0, names["semaphore_flag_evaluations_total"], 0)
			assert.InDelta(t, 1.0, names["semaphore_get_manifest_total"], 0)
			assert.Contains(t, names, "semaphore_build_info")
			assert.Contains(t, names, "go_goroutines", "metrics of the Go runtime are never renamed")
			if tt.exposeLegacy {
				assert.InDelta(t, 1.0, names["gofeatureflag_flag_evaluations_total"], 0)
				assert.InDelta(t, 1.0, names["gofeatureflag_get_manifest_total"], 0)
				assert.Contains(t, names, "gofeatureflag_build_info")
				assert.NotContains(t, names, "gofeatureflag_go_goroutines")
			} else {
				assert.NotContains(t, names, "gofeatureflag_flag_evaluations_total")
				assert.NotContains(t, names, "gofeatureflag_build_info")
			}
		})
	}
}

// countingCollector exposes a gauge that increases at every collection, like a collector with a side effect.
type countingCollector struct {
	desc  *prom.Desc
	calls atomic.Int64
}

func (c *countingCollector) Describe(ch chan<- *prom.Desc) { ch <- c.desc }

func (c *countingCollector) Collect(ch chan<- prom.Metric) {
	ch <- prom.MustNewConstMetric(c.desc, prom.GaugeValue, float64(c.calls.Add(1)))
}

func TestMetrics_GathererCollectsOnce(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		exposeLegacy bool
	}{
		{name: "semaphore series only", exposeLegacy: false},
		{name: "with the legacy series", exposeLegacy: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m, err := metric.NewMetrics()
			require.NoError(t, err)
			c := &countingCollector{desc: prom.NewDesc("semaphore_counting", "a collection counter", nil, nil)}
			require.NoError(t, m.Registry.Register(c))

			families, err := m.Gatherer(tt.exposeLegacy).Gather()
			require.NoError(t, err)

			assert.Equal(t, int64(1), c.calls.Load(), "a scrape collects once")
			values := map[string]float64{}
			names := make([]string, 0, len(families))
			for _, f := range families {
				names = append(names, f.GetName())
				if len(f.GetMetric()) == 1 && f.GetMetric()[0].GetGauge() != nil {
					values[f.GetName()] = f.GetMetric()[0].GetGauge().GetValue()
				}
			}
			assert.IsIncreasing(t, names, "the families are sorted by name")
			assert.InDelta(t, 1.0, values["semaphore_counting"], 0)
			if tt.exposeLegacy {
				assert.InDelta(t, 1.0, values["gofeatureflag_counting"], 0, "both names carry the same collection")
			}
		})
	}
}

func TestMetrics_RenamedMetricsKeepTheirLegacyNames(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		inc          func(m *metric.Metrics)
		wantName     string
		wantLegacy   string
		wantNotNamed string
	}{
		{
			name:         "per flag update",
			inc:          func(m *metric.Metrics) { m.IncFlagUpdated("f") },
			wantName:     "semaphore_flag_update_by_flag_total",
			wantLegacy:   "gofeatureflag_flag_update",
			wantNotNamed: "semaphore_flag_update",
		},
		{
			name:         "per flag delete",
			inc:          func(m *metric.Metrics) { m.IncFlagDeleted("f") },
			wantName:     "semaphore_flag_delete_by_flag_total",
			wantLegacy:   "gofeatureflag_flag_delete",
			wantNotNamed: "semaphore_flag_delete",
		},
		{
			name:         "per flag create",
			inc:          func(m *metric.Metrics) { m.IncFlagCreated("f") },
			wantName:     "semaphore_flag_create_by_flag_total",
			wantLegacy:   "gofeatureflag_flag_create",
			wantNotNamed: "semaphore_flag_create",
		},
		{
			name:         "force refresh",
			inc:          func(m *metric.Metrics) { m.IncForceRefresh() },
			wantName:     "semaphore_force_refresh_total",
			wantLegacy:   "gofeatureflag_force_refresh",
			wantNotNamed: "semaphore_force_refresh",
		},
		{
			name:         "bulk evaluations with flag names",
			inc:          func(m *metric.Metrics) { m.IncAllFlag("f") },
			wantName:     "semaphore_all_flags_evaluations_with_flag_total",
			wantLegacy:   "gofeatureflag_all_flags_evaluations_total_with_flag",
			wantNotNamed: "semaphore_all_flags_evaluations_total_with_flag",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m, err := metric.NewMetrics(metric.MetricsOpts{EnableBulkMetricFlagNames: true})
			require.NoError(t, err)
			tt.inc(&m)

			families, err := m.Gatherer(true).Gather()
			require.NoError(t, err)
			names := map[string]bool{}
			for _, f := range families {
				names[f.GetName()] = true
			}

			assert.True(t, names[tt.wantName], tt.wantName)
			assert.True(t, names[tt.wantLegacy], tt.wantLegacy)
			assert.False(t, names[tt.wantNotNamed], tt.wantNotNamed)
		})
	}
}

package metric

import (
	"testing"

	prom "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMetrics_IncAllFlag(t *testing.T) {
	metricSrv, err := NewMetrics()
	require.NoError(t, err)

	metricSrv.IncAllFlag()
	metricSrv.IncAllFlag()
	metricSrv.IncAllFlag()

	assert.InDelta(t, 3.0, testutil.ToFloat64(metricSrv.allFlagCounter), 0)
}

func TestMetrics_IncAllFlag_WithBulkMetricEnabled(t *testing.T) {
	metricSrv, err := NewMetrics(MetricsOpts{
		EnableBulkMetricFlagNames: true,
	})
	require.NoError(t, err)
	metricSrv.IncAllFlag("test-flag")
	metricSrv.IncAllFlag("test-flag")
	metricSrv.IncAllFlag("test-flag", "test-flag2")

	assert.InDelta(
		t,
		3.0,
		testutil.ToFloat64(metricSrv.allFlagCounterWithFlag.WithLabelValues("test-flag")),
		0,
	)
	assert.InDelta(
		t,
		1.0,
		testutil.ToFloat64(metricSrv.allFlagCounterWithFlag.WithLabelValues("test-flag2")),
		0,
	)
	assert.InDelta(t, 3.0, testutil.ToFloat64(metricSrv.allFlagCounter), 0)
}

func TestMetrics_IncCollectEvalData(t *testing.T) {
	metricSrv, err := NewMetrics()
	require.NoError(t, err)

	metricSrv.IncCollectEvalData(123)
	metricSrv.IncCollectEvalData(2)

	assert.InDelta(t, 125.0, testutil.ToFloat64(metricSrv.collectEvalDataCounter), 0)
}

func TestMetrics_IncFlagEvaluation(t *testing.T) {
	metricSrv, err := NewMetrics()
	require.NoError(t, err)

	metricSrv.IncFlagEvaluation("test-flag")
	metricSrv.IncFlagEvaluation("test-flag")
	metricSrv.IncFlagEvaluation("test-flag2")

	assert.InDelta(
		t,
		2.0,
		testutil.ToFloat64(metricSrv.flagEvaluationCounter.WithLabelValues("test-flag")),
		0,
	)
	assert.InDelta(
		t,
		1.0,
		testutil.ToFloat64(metricSrv.flagEvaluationCounter.WithLabelValues("test-flag2")),
		0,
	)
}

func TestMetrics_IncFlagNotFoundEvaluation(t *testing.T) {
	metricSrv, err := NewMetrics()
	require.NoError(t, err)

	metricSrv.IncFlagNotFoundEvaluation()
	metricSrv.IncFlagNotFoundEvaluation()

	// all the flags that are not found are counted in the same time series
	assert.Equal(t, 1, testutil.CollectAndCount(metricSrv.flagEvaluationCounter))
	assert.InDelta(
		t,
		2.0,
		testutil.ToFloat64(metricSrv.flagEvaluationCounter.WithLabelValues(FlagNotFoundLabelValue)),
		0,
	)
}

func TestMetrics_IncFlagCreated(t *testing.T) {
	metricSrv, err := NewMetrics()
	require.NoError(t, err)

	metricSrv.IncFlagCreated("test-flag")
	metricSrv.IncFlagCreated("test-flag2")

	assert.InDelta(t, 2.0, testutil.ToFloat64(metricSrv.flagCreateCounter), 0)
	assert.InDelta(
		t,
		1.0,
		testutil.ToFloat64(metricSrv.flagCreateCounterVec.WithLabelValues("test-flag2")),
		0,
	)
	assert.InDelta(
		t,
		1.0,
		testutil.ToFloat64(metricSrv.flagCreateCounterVec.WithLabelValues("test-flag")),
		0,
	)
}

func TestMetrics_IncFlagUpdated(t *testing.T) {
	metricSrv, err := NewMetrics()
	require.NoError(t, err)

	metricSrv.IncFlagUpdated("test-flag")
	metricSrv.IncFlagUpdated("test-flag2")

	assert.InDelta(t, 2.0, testutil.ToFloat64(metricSrv.flagUpdateCounter), 0)
	assert.InDelta(
		t,
		1.0,
		testutil.ToFloat64(metricSrv.flagUpdateCounterVec.WithLabelValues("test-flag2")),
		0,
	)
	assert.InDelta(
		t,
		1.0,
		testutil.ToFloat64(metricSrv.flagUpdateCounterVec.WithLabelValues("test-flag")),
		0,
	)
}

func TestMetrics_IncFlagDeleted(t *testing.T) {
	metricSrv, err := NewMetrics()
	require.NoError(t, err)

	metricSrv.IncFlagDeleted("test-flag")
	metricSrv.IncFlagDeleted("test-flag2")

	assert.InDelta(t, 2.0, testutil.ToFloat64(metricSrv.flagDeleteCounter), 0)
	assert.InDelta(
		t,
		1.0,
		testutil.ToFloat64(metricSrv.flagDeleteCounterVec.WithLabelValues("test-flag2")),
		0,
	)
	assert.InDelta(
		t,
		1.0,
		testutil.ToFloat64(metricSrv.flagDeleteCounterVec.WithLabelValues("test-flag")),
		0,
	)
}

func TestMetrics_IncFlagChange(t *testing.T) {
	metricSrv, err := NewMetrics()
	require.NoError(t, err)

	metricSrv.IncFlagChange()
	metricSrv.IncFlagChange()
	metricSrv.IncFlagChange()

	assert.InDelta(t, 3.0, testutil.ToFloat64(metricSrv.flagChange), 0)
}

func TestMetrics_IncForceRefresh(t *testing.T) {
	metricSrv, err := NewMetrics()
	require.NoError(t, err)

	metricSrv.IncForceRefresh()
	metricSrv.IncForceRefresh()
	metricSrv.IncForceRefresh()

	assert.InDelta(t, 3.0, testutil.ToFloat64(metricSrv.forceRefreshCounter), 0)
}

func TestMetrics_IncValidateFlags(t *testing.T) {
	metricSrv, err := NewMetrics()
	require.NoError(t, err)

	metricSrv.IncValidateFlags(ValidateFlagsOutcomeValid)
	metricSrv.IncValidateFlags(ValidateFlagsOutcomeValid)
	metricSrv.IncValidateFlags(ValidateFlagsOutcomeInvalid)
	metricSrv.IncValidateFlags(ValidateFlagsOutcomeError)

	counter := func(outcome string) float64 {
		return testutil.ToFloat64(metricSrv.validateFlagsCounterVec.With(prom.Labels{outcomeLabel: outcome}))
	}
	assert.InDelta(t, 2.0, counter(ValidateFlagsOutcomeValid), 0)
	assert.InDelta(t, 1.0, counter(ValidateFlagsOutcomeInvalid), 0)
	assert.InDelta(t, 1.0, counter(ValidateFlagsOutcomeError), 0)

	// a zero value Metrics must not panic
	assert.NotPanics(t, func() { (&Metrics{}).IncValidateFlags(ValidateFlagsOutcomeValid) })
}

func TestMetrics_IncFlagConfigurationCall(t *testing.T) {
	metricSrv, err := NewMetrics()
	require.NoError(t, err)

	metricSrv.IncFlagConfigurationCall()
	metricSrv.IncFlagConfigurationCall()
	metricSrv.IncFlagConfigurationCall()

	assert.InDelta(t, 3.0, testutil.ToFloat64(metricSrv.flagConfigurationCounter), 0)
}

func TestMetrics_IncGetManifestCall(t *testing.T) {
	metricSrv, err := NewMetrics()
	require.NoError(t, err)

	metricSrv.IncGetManifestCall()
	metricSrv.IncGetManifestCall()
	metricSrv.IncGetManifestCall()
	assert.InDelta(t, 3.0, testutil.ToFloat64(metricSrv.getManifestCounter), 0)
}

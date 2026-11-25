package metric

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/flag"
	"github.com/blackbirdworks/semaphore/notifier"
)

const (
	testFlagAdd1 = "test-flagAdd1"
	testFlagAdd2 = "test-flagAdd2"
	testFlagAdd3 = "test-flagAdd3"
)

func TestPrometheusNotifier_with_diff(t *testing.T) {
	t.Parallel()

	metrics, err := NewMetrics()
	require.NoError(t, err)

	diff := notifier.DiffCache{
		Deleted: map[string]flag.Flag{
			testFlag: &flag.InternalFlag{},
		},
		Updated: map[string]notifier.DiffUpdated{
			testFlag2: {
				Before: &flag.InternalFlag{},
				After:  &flag.InternalFlag{},
			},
		},
		Added: map[string]flag.Flag{
			testFlagAdd1: &flag.InternalFlag{},
			testFlagAdd2: &flag.InternalFlag{},
			testFlagAdd3: &flag.InternalFlag{},
		},
	}

	n := NewPrometheusNotifier(metrics)
	err = n.Notify(diff)
	require.NoError(t, err)

	assert.InDelta(t, 1.0, testutil.ToFloat64(metrics.flagDeleteCounter), 0)
	assert.InDelta(t, 1.0, testutil.ToFloat64(metrics.flagUpdateCounter), 0)
	assert.InDelta(t, 3.0, testutil.ToFloat64(metrics.flagCreateCounter), 0)
	assert.InDelta(
		t,
		1.0,
		testutil.ToFloat64(metrics.flagCreateCounterVec.WithLabelValues(testFlagAdd1)),
		0,
	)
	assert.InDelta(
		t,
		1.0,
		testutil.ToFloat64(metrics.flagCreateCounterVec.WithLabelValues(testFlagAdd2)),
		0,
	)
	assert.InDelta(
		t,
		1.0,
		testutil.ToFloat64(metrics.flagCreateCounterVec.WithLabelValues(testFlagAdd3)),
		0,
	)
	assert.InDelta(t, 1.0, testutil.ToFloat64(metrics.flagUpdateCounterVec.WithLabelValues(testFlag2)), 0)
	assert.InDelta(t, 1.0, testutil.ToFloat64(metrics.flagDeleteCounterVec.WithLabelValues(testFlag)), 0)
	assert.InDelta(t, 1.0, testutil.ToFloat64(metrics.flagChange), 0)
}

func TestPrometheusNotifier_no_diff(t *testing.T) {
	t.Parallel()

	metrics, err := NewMetrics()
	require.NoError(t, err)

	diff := notifier.DiffCache{
		Deleted: map[string]flag.Flag{},
		Updated: map[string]notifier.DiffUpdated{},
		Added:   map[string]flag.Flag{},
	}

	n := NewPrometheusNotifier(metrics)
	err = n.Notify(diff)
	require.NoError(t, err)
	assert.InDelta(t, 0.0, testutil.ToFloat64(metrics.flagDeleteCounter), 0)
	assert.InDelta(t, 0.0, testutil.ToFloat64(metrics.flagUpdateCounter), 0)
	assert.InDelta(t, 0.0, testutil.ToFloat64(metrics.flagCreateCounter), 0)
	assert.InDelta(
		t,
		0.0,
		testutil.ToFloat64(metrics.flagCreateCounterVec.WithLabelValues(testFlagAdd1)),
		0,
	)
	assert.InDelta(
		t,
		0.0,
		testutil.ToFloat64(metrics.flagCreateCounterVec.WithLabelValues(testFlagAdd2)),
		0,
	)
	assert.InDelta(
		t,
		0.0,
		testutil.ToFloat64(metrics.flagCreateCounterVec.WithLabelValues(testFlagAdd3)),
		0,
	)
	assert.InDelta(t, 0.0, testutil.ToFloat64(metrics.flagUpdateCounterVec.WithLabelValues(testFlag2)), 0)
	assert.InDelta(t, 0.0, testutil.ToFloat64(metrics.flagDeleteCounterVec.WithLabelValues(testFlag)), 0)
	assert.InDelta(t, 0.0, testutil.ToFloat64(metrics.flagChange), 0)
}

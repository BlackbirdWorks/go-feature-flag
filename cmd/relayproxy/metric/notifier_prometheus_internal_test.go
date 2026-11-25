package metric

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
	"github.com/blackbirdworks/semaphore/notifier"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

const (
	testFlagAddOne   = "test-flagAdd1"
	testFlagAddTwo   = "test-flagAdd2"
	testFlagAddThree = "test-flagAdd3"
)

func TestPrometheusNotifier_with_diff(t *testing.T) {
	t.Parallel()

	metrics, err := NewMetrics()
	require.NoError(t, err)

	diff := notifier.DiffCache{
		Deleted: map[string]flagdef.Flag{
			fixtures.FlagKeyTest: &flagdef.InternalFlag{},
		},
		Updated: map[string]notifier.DiffUpdated{
			fixtures.FlagKeyTestSecond: {
				Before: &flagdef.InternalFlag{},
				After:  &flagdef.InternalFlag{},
			},
		},
		Added: map[string]flagdef.Flag{
			testFlagAddOne:   &flagdef.InternalFlag{},
			testFlagAddTwo:   &flagdef.InternalFlag{},
			testFlagAddThree: &flagdef.InternalFlag{},
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
		testutil.ToFloat64(metrics.flagCreateCounterVec.WithLabelValues(testFlagAddOne)),
		0,
	)
	assert.InDelta(
		t,
		1.0,
		testutil.ToFloat64(metrics.flagCreateCounterVec.WithLabelValues(testFlagAddTwo)),
		0,
	)
	assert.InDelta(
		t,
		1.0,
		testutil.ToFloat64(metrics.flagCreateCounterVec.WithLabelValues(testFlagAddThree)),
		0,
	)
	assert.InDelta(
		t,
		1.0,
		testutil.ToFloat64(metrics.flagUpdateCounterVec.WithLabelValues(fixtures.FlagKeyTestSecond)),
		0,
	)
	assert.InDelta(t, 1.0, testutil.ToFloat64(metrics.flagDeleteCounterVec.WithLabelValues(fixtures.FlagKeyTest)), 0)
	assert.InDelta(t, 1.0, testutil.ToFloat64(metrics.flagChange), 0)
}

func TestPrometheusNotifier_no_diff(t *testing.T) {
	t.Parallel()

	metrics, err := NewMetrics()
	require.NoError(t, err)

	diff := notifier.DiffCache{
		Deleted: map[string]flagdef.Flag{},
		Updated: map[string]notifier.DiffUpdated{},
		Added:   map[string]flagdef.Flag{},
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
		testutil.ToFloat64(metrics.flagCreateCounterVec.WithLabelValues(testFlagAddOne)),
		0,
	)
	assert.InDelta(
		t,
		0.0,
		testutil.ToFloat64(metrics.flagCreateCounterVec.WithLabelValues(testFlagAddTwo)),
		0,
	)
	assert.InDelta(
		t,
		0.0,
		testutil.ToFloat64(metrics.flagCreateCounterVec.WithLabelValues(testFlagAddThree)),
		0,
	)
	assert.InDelta(
		t,
		0.0,
		testutil.ToFloat64(metrics.flagUpdateCounterVec.WithLabelValues(fixtures.FlagKeyTestSecond)),
		0,
	)
	assert.InDelta(t, 0.0, testutil.ToFloat64(metrics.flagDeleteCounterVec.WithLabelValues(fixtures.FlagKeyTest)), 0)
	assert.InDelta(t, 0.0, testutil.ToFloat64(metrics.flagChange), 0)
}

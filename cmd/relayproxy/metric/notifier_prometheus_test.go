package metric

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/flag"
	"github.com/blackbirdworks/semaphore/notifier"
)

func TestPrometheusNotifier_with_diff(t *testing.T) {
	m, err := NewMetrics()
	require.NoError(t, err)

	diff := notifier.DiffCache{
		Deleted: map[string]flag.Flag{
			"test-flag": &flag.InternalFlag{},
		},
		Updated: map[string]notifier.DiffUpdated{
			"test-flag2": {
				Before: &flag.InternalFlag{},
				After:  &flag.InternalFlag{},
			},
		},
		Added: map[string]flag.Flag{
			"test-flagAdd1": &flag.InternalFlag{},
			"test-flagAdd2": &flag.InternalFlag{},
			"test-flagAdd3": &flag.InternalFlag{},
		},
	}

	n := NewPrometheusNotifier(m)
	err = n.Notify(diff)
	require.NoError(t, err)

	assert.InDelta(t, 1.0, testutil.ToFloat64(m.flagDeleteCounter), 0)
	assert.InDelta(t, 1.0, testutil.ToFloat64(m.flagUpdateCounter), 0)
	assert.InDelta(t, 3.0, testutil.ToFloat64(m.flagCreateCounter), 0)
	assert.InDelta(
		t,
		1.0,
		testutil.ToFloat64(m.flagCreateCounterVec.WithLabelValues("test-flagAdd1")),
		0,
	)
	assert.InDelta(
		t,
		1.0,
		testutil.ToFloat64(m.flagCreateCounterVec.WithLabelValues("test-flagAdd2")),
		0,
	)
	assert.InDelta(
		t,
		1.0,
		testutil.ToFloat64(m.flagCreateCounterVec.WithLabelValues("test-flagAdd3")),
		0,
	)
	assert.InDelta(t, 1.0, testutil.ToFloat64(m.flagUpdateCounterVec.WithLabelValues("test-flag2")), 0)
	assert.InDelta(t, 1.0, testutil.ToFloat64(m.flagDeleteCounterVec.WithLabelValues("test-flag")), 0)
	assert.InDelta(t, 1.0, testutil.ToFloat64(m.flagChange), 0)
}

func TestPrometheusNotifier_no_diff(t *testing.T) {
	m, err := NewMetrics()
	require.NoError(t, err)

	diff := notifier.DiffCache{
		Deleted: map[string]flag.Flag{},
		Updated: map[string]notifier.DiffUpdated{},
		Added:   map[string]flag.Flag{},
	}

	n := NewPrometheusNotifier(m)
	err = n.Notify(diff)
	require.NoError(t, err)
	assert.InDelta(t, 0.0, testutil.ToFloat64(m.flagDeleteCounter), 0)
	assert.InDelta(t, 0.0, testutil.ToFloat64(m.flagUpdateCounter), 0)
	assert.InDelta(t, 0.0, testutil.ToFloat64(m.flagCreateCounter), 0)
	assert.InDelta(
		t,
		0.0,
		testutil.ToFloat64(m.flagCreateCounterVec.WithLabelValues("test-flagAdd1")),
		0,
	)
	assert.InDelta(
		t,
		0.0,
		testutil.ToFloat64(m.flagCreateCounterVec.WithLabelValues("test-flagAdd2")),
		0,
	)
	assert.InDelta(
		t,
		0.0,
		testutil.ToFloat64(m.flagCreateCounterVec.WithLabelValues("test-flagAdd3")),
		0,
	)
	assert.InDelta(t, 0.0, testutil.ToFloat64(m.flagUpdateCounterVec.WithLabelValues("test-flag2")), 0)
	assert.InDelta(t, 0.0, testutil.ToFloat64(m.flagDeleteCounterVec.WithLabelValues("test-flag")), 0)
	assert.InDelta(t, 0.0, testutil.ToFloat64(m.flagChange), 0)
}

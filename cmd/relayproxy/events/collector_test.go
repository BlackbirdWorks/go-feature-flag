package events_test

import (
	"context"
	"slices"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/events"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/notifier"
)

// series is one sample of a metric family: its labels and its value.
type series struct {
	labels map[string]string
	value  int
}

func gather(tb testing.TB, source events.StatusReader) map[string][]series {
	tb.Helper()

	reg := prometheus.NewPedanticRegistry()
	require.NoError(tb, reg.Register(events.NewCollector(source)))

	families, err := reg.Gather() // fails on a duplicate series or an inconsistent label set
	require.NoError(tb, err)

	out := map[string][]series{}

	for _, family := range families {
		for _, metric := range family.GetMetric() {
			labels := map[string]string{}
			for _, pair := range metric.GetLabel() {
				labels[pair.GetName()] = pair.GetValue()
			}

			value := int(metric.GetGauge().GetValue() + metric.GetCounter().GetValue())
			if family.GetType() == dto.MetricType_SUMMARY {
				value = int(metric.GetSummary().GetSampleSum())
				labels["count"] = "" // the sample count is checked apart
			}

			out[family.GetName()] = append(out[family.GetName()], series{labels: labels, value: value})
		}
	}

	return out
}

func find(tb testing.TB, all []series, want map[string]string) series {
	tb.Helper()

	for _, candidate := range all {
		match := true

		for k, v := range want {
			if candidate.labels[k] != v {
				match = false
			}
		}

		if match {
			return candidate
		}
	}

	require.FailNow(tb, "series not found")

	return series{}
}

func TestCollector(t *testing.T) {
	t.Parallel()

	fac := newFactory()
	runtime := events.NewRuntime(&events.RuntimeConfig{NewManager: fac.build, QueueSize: 4, Workers: 1, BatchSize: 1})
	changes := make(chan flagruntime.Change)

	require.NoError(t, runtime.Start(context.Background(), changes))
	t.Cleanup(func() { _, _ = runtime.Shutdown(canceled()) })

	_, err := runtime.Dispatcher.Apply(context.Background(), []events.ExporterDef{def("a", "v1"), def("b", "v1")})
	require.NoError(t, err)

	got := make(chan notifier.DiffCache, 2)
	_, err = runtime.Notifications.Apply(
		context.Background(),
		[]events.NotifierDef{ndef("hook", "v1", &recordingNotifier{got: got})},
	)
	require.NoError(t, err)

	for i := range 3 {
		require.True(t, runtime.Collect(ev(i)))
	}

	fac.of("a", 0).waitFor(t, 3)
	fac.of("b", 0).waitFor(t, 3)

	changes <- change("set", 1)

	await(t, got, "the notifier")

	// replace "a": the old one is retired and has the name of the running one
	_, err = runtime.Dispatcher.Apply(context.Background(), []events.ExporterDef{def("a", "v2"), def("b", "v1")})
	require.NoError(t, err)

	metrics := gather(t, runtime)

	wantNames := []string{
		"semaphore_events_accepted_total", "semaphore_events_dropped_total", "semaphore_events_queue_capacity",
		"semaphore_events_queue_length", "semaphore_exporter_dropped_events_total",
		"semaphore_exporter_enqueued_events_total", "semaphore_exporter_exported_events_total",
		"semaphore_exporter_flush_duration_seconds", "semaphore_exporter_flush_failures_total",
		"semaphore_exporter_queued_events", "semaphore_exporter_state", "semaphore_notifier_changes_total",
		"semaphore_notifier_queued_changes",
	}

	names := make([]string, 0, len(metrics))
	for name := range metrics {
		names = append(names, name)
	}

	slices.Sort(names)
	assert.Equal(t, wantNames, names)

	// the label sets are closed: no flag, no flag set, no event value
	allowed := map[string]bool{
		labelExporter: true,
		"kind":        true,
		"reason":      true,
		labelState:    true,
		labelNotifier: true,
		"result":      true,
		"count":       true,
	}

	for name, all := range metrics {
		for _, s := range all {
			for label := range s.labels {
				assert.True(t, allowed[label], "%s has the label %s", name, label)
			}
		}
	}

	assert.Equal(t, 4, metrics["semaphore_events_queue_capacity"][0].value)
	assert.Equal(t, 3, metrics["semaphore_events_accepted_total"][0].value)
	assert.Equal(
		t,
		3,
		find(
			t,
			metrics["semaphore_exporter_enqueued_events_total"],
			map[string]string{labelExporter: "b", "kind": "file"},
		).value,
	)
	assert.Equal(
		t,
		1,
		find(
			t,
			metrics["semaphore_exporter_state"],
			map[string]string{labelExporter: "b", labelState: events.StateRunning},
		).value,
	)
	assert.Equal(
		t,
		1,
		find(
			t,
			metrics["semaphore_notifier_changes_total"],
			map[string]string{labelNotifier: "hook", "result": "delivered"},
		).value,
	)
	assert.Equal(
		t,
		0,
		find(
			t,
			metrics["semaphore_exporter_state"],
			map[string]string{labelExporter: "a", labelState: events.StateStopped},
		).value,
		"the retired exporter with the name of a running one is not repeated",
	)
	assert.Len(t, metrics["semaphore_exporter_queued_events"], 2, "one series per exporter name")
}

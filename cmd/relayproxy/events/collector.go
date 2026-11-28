package events

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Metric names. The labels are closed: exporter and notifier are the names of the entries of the bootstrap file
// (bounded by the configuration), kind is the kind of the entry, reason, state and result come from fixed sets.
// No label carries a flag, a flag set or an event value.
const (
	metricQueueCapacity  = "semaphore_events_queue_capacity"
	metricQueueLength    = "semaphore_events_queue_length"
	metricAccepted       = "semaphore_events_accepted_total"
	metricDropped        = "semaphore_events_dropped_total"
	metricQueued         = "semaphore_exporter_queued_events"
	metricHanded         = "semaphore_exporter_enqueued_events_total"
	metricExported       = "semaphore_exporter_exported_events_total"
	metricExporterDrops  = "semaphore_exporter_dropped_events_total"
	metricFlushFailures  = "semaphore_exporter_flush_failures_total"
	metricFlushDuration  = "semaphore_exporter_flush_duration_seconds"
	metricExporterState  = "semaphore_exporter_state"
	metricNotifierQueued = "semaphore_notifier_queued_changes"
	metricNotifierTotal  = "semaphore_notifier_changes_total"
)

// Collector exposes a [Status] to Prometheus. Register it in the registry of the relay when the relay switches
// to config v2; it is not part of the metrics of version 1.
type Collector struct {
	source StatusReader

	queueCapacity, queueLength, accepted, dropped                *prometheus.Desc
	queued, handed, exported, exporterDrops, failures, flushTime *prometheus.Desc
	exporterState, notifierQueued, notifierTotal                 *prometheus.Desc
}

// NewCollector returns the collector of the status of source.
func NewCollector(source StatusReader) *Collector {
	exp := []string{"exporter", "kind"}
	notifLabels := []string{"notifier", "kind", "result"}
	desc := func(name, help string, labels []string) *prometheus.Desc {
		return prometheus.NewDesc(name, help, labels, nil)
	}

	return &Collector{
		source:        source,
		queueCapacity: desc(metricQueueCapacity, "Capacity of the event channel.", nil),
		queueLength:   desc(metricQueueLength, "Events in the event channel.", nil),
		accepted:      desc(metricAccepted, "Events put on the event channel.", nil),
		dropped: desc(
			metricDropped,
			"Events dropped before they reached an exporter, by reason.",
			[]string{"reason"},
		),
		queued:        desc(metricQueued, "Events handed to the exporter and not yet exported or dropped.", exp),
		handed:        desc(metricHanded, "Events handed to the exporter.", exp),
		exported:      desc(metricExported, "Events the exporter wrote.", exp),
		exporterDrops: desc(metricExporterDrops, "Events the exporter dropped, by reason.", append(exp, "reason")),
		failures:      desc(metricFlushFailures, "Failed flushes of the exporter.", exp),
		flushTime:     desc(metricFlushDuration, "Duration of the flushes of the exporter.", exp),
		exporterState: desc(
			metricExporterState,
			"State of the exporter: 1 for its current state.",
			append(exp, "state"),
		),
		notifierQueued: desc(metricNotifierQueued, "Changes waiting for the notifier.", notifLabels[:2]),
		notifierTotal: desc(
			metricNotifierTotal,
			"Changes by outcome for the notifier.",
			notifLabels,
		),
	}
}

// Describe implements [prometheus.Collector].
func (c *Collector) Describe(out chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{
		c.queueCapacity, c.queueLength, c.accepted, c.dropped, c.queued, c.handed, c.exported, c.exporterDrops,
		c.failures, c.flushTime, c.exporterState, c.notifierQueued, c.notifierTotal,
	} {
		out <- d
	}
}

// Collect implements [prometheus.Collector].
func (c *Collector) Collect(out chan<- prometheus.Metric) {
	snap := c.source.Status()

	gauge := func(d *prometheus.Desc, v float64, labels ...string) {
		out <- prometheus.MustNewConstMetric(d, prometheus.GaugeValue, v, labels...)
	}
	counter := func(d *prometheus.Desc, v uint64, labels ...string) {
		out <- prometheus.MustNewConstMetric(d, prometheus.CounterValue, float64(v), labels...)
	}

	gauge(c.queueCapacity, float64(snap.Queue.Capacity))
	gauge(c.queueLength, float64(snap.Queue.Length))
	counter(c.accepted, snap.Queue.Accepted)
	counter(c.dropped, snap.Queue.Dropped.QueueFull, ReasonQueueFull)
	counter(c.dropped, snap.Queue.Dropped.Shutdown, ReasonShutdown)
	counter(c.dropped, snap.Queue.Dropped.NoExporter, ReasonNoExporter)
	counter(c.dropped, snap.Queue.Dropped.Drain, ReasonDrain)
	counter(c.dropped, snap.Queue.Dropped.NotPublic, ReasonNotPublic)

	for i := range snap.Exporters {
		c.collectExporter(out, &snap.Exporters[i], gauge, counter)
	}

	for i := range snap.Notifiers {
		note := &snap.Notifiers[i]

		gauge(c.notifierQueued, float64(note.Queued), note.Name, note.Kind)
		counter(c.notifierTotal, note.Delivered, note.Name, note.Kind, "delivered")
		counter(c.notifierTotal, note.Errors, note.Name, note.Kind, "error")
		counter(c.notifierTotal, note.Panics, note.Name, note.Kind, "panic")
		counter(c.notifierTotal, note.Dropped, note.Name, note.Kind, "dropped")
	}
}

func (c *Collector) collectExporter(
	out chan<- prometheus.Metric, exp *ExporterStatus,
	gauge func(*prometheus.Desc, float64, ...string), counter func(*prometheus.Desc, uint64, ...string),
) {
	gauge(c.queued, float64(exp.Queued), exp.Name, exp.Kind)
	counter(c.handed, exp.Handed, exp.Name, exp.Kind)
	counter(c.exported, exp.Exported, exp.Name, exp.Kind)
	counter(c.exporterDrops, exp.Dropped.Overflow, exp.Name, exp.Kind, ReasonOverflow)
	counter(c.exporterDrops, exp.Dropped.Drain, exp.Name, exp.Kind, ReasonDrain)
	counter(c.failures, exp.FlushFailures, exp.Name, exp.Kind)

	for _, state := range []string{StateRunning, StateDraining, StateStopped, StateCut} {
		value := 0.0
		if exp.State == state {
			value = 1
		}

		gauge(c.exporterState, value, exp.Name, exp.Kind, state)
	}

	out <- prometheus.MustNewConstSummary(c.flushTime, exp.Flushes, exp.FlushSeconds, nil, exp.Name, exp.Kind)
}

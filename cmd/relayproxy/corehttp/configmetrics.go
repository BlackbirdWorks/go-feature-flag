package corehttp

import (
	"strconv"

	prom "github.com/prometheus/client_golang/prometheus"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/relaycore"
	"github.com/blackbirdworks/semaphore/modules/core/clock"
)

// Metric names of the config v2 runtime ([MetricNames]).
const (
	metricGeneration  = "semaphore_config_generation"
	metricSnapshot    = "semaphore_config_snapshot"
	metricReloads     = "semaphore_config_reload_total"
	metricLastSuccess = "semaphore_config_last_success_timestamp_seconds"
	metricAge         = "semaphore_config_age_seconds"
	metricResources   = "semaphore_resource_count"
	metricResource    = "semaphore_resource_status"
	metricPending     = "semaphore_config_pending_restart"
	metricRejected    = "semaphore_flag_rejected_total"
	metricPresent     = "semaphore_flags_rejected"
	metricObjects     = "semaphore_object_status"
	metricObjectAge   = "semaphore_object_oldest_unapplied_age_seconds"
)

// maxNamedResources caps the series of semaphore_resource_status: one per resource that is not ready, the first
// ones by name. The counts in semaphore_resource_count are never capped.
const maxNamedResources = 100

// objectStates are the states of a source object, as /info reports them.
func objectStates() []string {
	return []string{
		string(flagruntime.ObjectOK), string(flagruntime.ObjectStale), string(flagruntime.ObjectMissing),
		string(flagruntime.ObjectPending), string(flagruntime.ObjectInvalid),
	}
}

// nanosPerSecond converts a Unix time in nanoseconds to seconds.
const nanosPerSecond = 1e9

// MetricNames are the metrics this collector exports.
func MetricNames() []string {
	return []string{
		metricGeneration, metricSnapshot, metricReloads, metricLastSuccess, metricAge, metricResources,
		metricResource, metricPending, metricRejected, metricPresent, metricObjects, metricObjectAge,
	}
}

// ConfigCollector exposes the status of a core: generations, reloads by outcome, staleness and the resources that
// are not ready. Series by resource name exist only for resources that are not ready (capped), the counts by state
// always exist, so the number of series follows the problems, not the flag sets, and never the flags: a scrape reads
// a summary that was made when the runtime was swapped, and evaluates no flag.
type ConfigCollector struct {
	src  StatusSource
	sums *summaries

	generation, snapshot, reloads, lastSuccess, age, counts, named, pending *prom.Desc
	rejected, present, objects, objectAge                                   *prom.Desc
}

var _ prom.Collector = (*ConfigCollector)(nil)

// NewConfigCollector returns the collector of the status of src.
func NewConfigCollector(src StatusSource) *ConfigCollector {
	desc := func(name, help string, labels ...string) *prom.Desc { return prom.NewDesc(name, help, labels, nil) }

	return &ConfigCollector{
		src:  src,
		sums: &summaries{},
		generation: desc(metricGeneration, "Config generation by layer: applied (served) and received (handed to the "+
			"engine).", "layer"),
		snapshot:    desc(metricSnapshot, "Local counter of the swaps of the runtime."),
		reloads:     desc(metricReloads, "Reconcile rounds by result: applied, rejected, ignored.", "result"),
		lastSuccess: desc(metricLastSuccess, "Unix time of the last applied round; 0 when none."),
		age: desc(
			metricAge,
			"Seconds since the control plane last confirmed the manifest.",
		),
		counts: desc(
			metricResources, "Resources by kind (flagset, object, dynamicSegment) and state.", "kind", "state",
		),
		named: desc(metricResource, "1 for a resource that is not ready, by kind and name; at most "+
			strconv.Itoa(maxNamedResources)+" per kind.", "kind", "name", "state"),
		pending: desc(metricPending, "Settings that changed in the bootstrap file and need a restart."),
		rejected: desc(
			metricRejected,
			"Flags rejected at load, by what became of them: kept (the last valid definition "+
				"keeps serving) or dropped (none to fall back on). Counted once per rejected flag, in the applied "+
				"generation that brought the rejection.",
			"reason",
		),
		present: desc(metricPresent, "Rejected flags present in the running generation, by what became of them: kept "+
			"or dropped. Unlike the counter, it falls when the rejection is fixed.", "reason"),
		objects: desc(metricObjects, "Source objects by state: ok, stale, missing, pending, invalid.", "state"),
		objectAge: desc(metricObjectAge, "Seconds since the oldest object that is not ok left that state; 0 when "+
			"every object is ok."),
	}
}

// NewConfigCollectorSharing returns the collector of the status of src that shares the summaries of the readiness: a
// runtime is summarised once for the metrics and the probes, not once for each.
func NewConfigCollectorSharing(src StatusSource, ready *Readiness) *ConfigCollector {
	col := NewConfigCollector(src)
	col.sums = ready.sums

	return col
}

// Describe implements [prom.Collector].
func (c *ConfigCollector) Describe(out chan<- *prom.Desc) {
	for _, d := range []*prom.Desc{
		c.generation, c.snapshot, c.reloads, c.lastSuccess, c.age, c.counts, c.named, c.pending,
		c.rejected, c.present, c.objects, c.objectAge,
	} {
		out <- d
	}
}

// Collect implements [prom.Collector].
func (c *ConfigCollector) Collect(out chan<- prom.Metric) {
	status := c.src.Status()
	gauge := func(d *prom.Desc, v float64, labels ...string) {
		out <- prom.MustNewConstMetric(d, prom.GaugeValue, v, labels...)
	}
	counter := func(d *prom.Desc, v float64, labels ...string) {
		out <- prom.MustNewConstMetric(d, prom.CounterValue, v, labels...)
	}

	current := status.Source.Runtime
	sum := c.sums.of(current)

	var applied, snapshot, pending float64
	if current != nil {
		applied, snapshot, pending = float64(current.Generation), float64(current.Snapshot),
			float64(len(current.PendingRestart))
	}

	gauge(c.generation, applied, "applied")
	gauge(c.generation, float64(status.Source.LastGeneration), "received")
	gauge(c.snapshot, snapshot)
	counter(c.reloads, float64(status.Reloads.Applied), string(flagruntime.OutcomeApplied))
	counter(c.reloads, float64(status.Reloads.Rejected), string(flagruntime.OutcomeRejected))
	counter(c.reloads, float64(status.Reloads.Ignored), string(flagruntime.OutcomeIgnored))

	last := 0.0
	if !status.Reloads.LastSuccess.IsZero() {
		last = float64(status.Reloads.LastSuccess.UnixNano()) / nanosPerSecond
	}

	gauge(c.lastSuccess, last)

	if sync := status.Source.Reader.LastSync; !sync.IsZero() {
		gauge(c.age, max(0, clock.Now().Sub(sync).Seconds()))
	}

	gauge(c.pending, pending)

	c.collectRejected(out, &status.Reloads, sum)

	for _, state := range []string{resourceReady, resourceDegraded, resourcePending} {
		gauge(c.counts, float64(sum.flagSets[state]), kindFlagSet, state)
	}

	for _, state := range objectStates() {
		gauge(c.counts, float64(sum.objects[state]), kindObject, state)
	}

	for _, state := range []string{stateValid, stateRejected} {
		gauge(c.counts, float64(sum.dynamic[state]), kindDynamicSegment, state)
	}

	for _, bad := range sum.badSets[:min(len(sum.badSets), maxNamedResources)] {
		gauge(c.named, 1, kindFlagSet, bad.name, bad.state)
	}

	for _, bad := range sum.badObject[:min(len(sum.badObject), maxNamedResources)] {
		gauge(c.named, 1, kindObject, bad.name, bad.state)
	}
}

// collectRejected exports the rejected flags (the counter and the gauge of the ones present) and the objects by state.
func (c *ConfigCollector) collectRejected(out chan<- prom.Metric, reloads *relaycore.ReloadStats, sum *summary) {
	gauge := func(d *prom.Desc, v float64, labels ...string) {
		out <- prom.MustNewConstMetric(d, prom.GaugeValue, v, labels...)
	}

	for _, reason := range []string{relaycore.RejectedKept, relaycore.RejectedDropped} {
		out <- prom.MustNewConstMetric(c.rejected, prom.CounterValue, float64(reloads.FlagsRejected[reason]), reason)

		gauge(c.present, float64(sum.rejected[reason]), reason)
	}

	for _, state := range objectStates() {
		gauge(c.objects, float64(sum.objects[state]), state)
	}

	oldest := 0.0
	if !sum.oldestNotOK.IsZero() {
		oldest = max(0, clock.Now().Sub(sum.oldestNotOK).Seconds())
	}

	gauge(c.objectAge, oldest)
}

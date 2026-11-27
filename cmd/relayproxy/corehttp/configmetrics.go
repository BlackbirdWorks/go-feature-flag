package corehttp

import (
	"fmt"

	prom "github.com/prometheus/client_golang/prometheus"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
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
)

// maxNamedResources caps the series of semaphore_resource_status: one per resource that is not ready, the first
// ones by name. The counts in semaphore_resource_count are never capped.
const maxNamedResources = 100

// MetricNames are the metrics this collector exports.
func MetricNames() []string {
	return []string{
		metricGeneration, metricSnapshot, metricReloads, metricLastSuccess, metricAge, metricResources,
		metricResource, metricPending,
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
}

var _ prom.Collector = (*ConfigCollector)(nil)

// NewConfigCollector returns the collector of the status of src.
func NewConfigCollector(src StatusSource) *ConfigCollector {
	desc := func(name, help string, labels ...string) *prom.Desc { return prom.NewDesc(name, help, labels, nil) }

	return &ConfigCollector{
		src: src, sums: &summaries{},
		generation: desc(metricGeneration, "Config generation by layer: applied (served) and received (handed to the "+
			"engine).", "layer"),
		snapshot: desc(metricSnapshot, "Local counter of the swaps of the runtime."),
		reloads: desc(metricReloads, "Reconcile rounds by result: applied, rejected, ignored.", "result"),
		lastSuccess: desc(metricLastSuccess, "Unix time of the last applied round; 0 when none."),
		age: desc(metricAge, "Seconds since the control plane last confirmed the held manifest; absent before the "+
			"first confirmation. It never makes the relay unready."),
		counts: desc(metricResources, "Resources by kind (flagset, object) and state.", "kind", "state"),
		named: desc(metricResource, "1 for a resource that is not ready, by kind and name; at most "+
			fmt.Sprint(maxNamedResources)+" per kind.", "kind", "name", "state"),
		pending: desc(metricPending, "Settings that changed in the bootstrap file and need a restart."),
	}
}

// Describe implements [prom.Collector].
func (c *ConfigCollector) Describe(ch chan<- *prom.Desc) {
	for _, d := range []*prom.Desc{c.generation, c.snapshot, c.reloads, c.lastSuccess, c.age, c.counts, c.named, c.pending} {
		ch <- d
	}
}

// Collect implements [prom.Collector].
func (c *ConfigCollector) Collect(ch chan<- prom.Metric) {
	st := c.src.Status()
	gauge := func(d *prom.Desc, v float64, labels ...string) {
		ch <- prom.MustNewConstMetric(d, prom.GaugeValue, v, labels...)
	}
	counter := func(d *prom.Desc, v float64, labels ...string) {
		ch <- prom.MustNewConstMetric(d, prom.CounterValue, v, labels...)
	}

	rt := st.Source.Runtime
	sum := c.sums.of(rt)

	var applied, snapshot, pending float64
	if rt != nil {
		applied, snapshot, pending = float64(rt.Generation), float64(rt.Snapshot), float64(len(rt.PendingRestart))
	}

	gauge(c.generation, applied, "applied")
	gauge(c.generation, float64(st.Source.LastGeneration), "received")
	gauge(c.snapshot, snapshot)
	counter(c.reloads, float64(st.Reloads.Applied), string(flagruntime.OutcomeApplied))
	counter(c.reloads, float64(st.Reloads.Rejected), string(flagruntime.OutcomeRejected))
	counter(c.reloads, float64(st.Reloads.Ignored), string(flagruntime.OutcomeIgnored))

	last := 0.0
	if !st.Reloads.LastSuccess.IsZero() {
		last = float64(st.Reloads.LastSuccess.UnixNano()) / 1e9
	}

	gauge(c.lastSuccess, last)

	if sync := st.Source.Reader.LastSync; !sync.IsZero() {
		gauge(c.age, max(0, clock.Now().Sub(sync).Seconds()))
	}

	gauge(c.pending, pending)

	for _, state := range []string{resourceReady, resourceDegraded, resourcePending} {
		gauge(c.counts, float64(sum.flagSets[state]), kindFlagSet, state)
	}

	for _, state := range []string{
		string(flagruntime.ObjectOK), string(flagruntime.ObjectStale), string(flagruntime.ObjectMissing),
		string(flagruntime.ObjectPending), string(flagruntime.ObjectInvalid),
	} {
		gauge(c.counts, float64(sum.objects[state]), kindObject, state)
	}

	for _, bad := range sum.badSets[:min(len(sum.badSets), maxNamedResources)] {
		gauge(c.named, 1, kindFlagSet, bad.name, bad.state)
	}

	for _, bad := range sum.badObject[:min(len(sum.badObject), maxNamedResources)] {
		gauge(c.named, 1, kindObject, bad.name, bad.state)
	}
}

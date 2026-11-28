package corehttp

import (
	prom "github.com/prometheus/client_golang/prometheus"
)

// The series of the dynamic segments. Labels are the connection (a name of the bootstrap file, a handful), the kind of
// result and the status: never a flag set, a segment or an identifier.
const (
	metricSegmentLookups = "semaphore_segment_lookups_total"
	metricSegmentCalls   = "semaphore_segment_store_calls_total"
	metricSegmentFailed  = "semaphore_segment_store_failed_calls_total"
	metricSegmentBreaker = "semaphore_segment_breaker_transitions_total"
	metricSegmentStore   = "semaphore_segment_store_state"
	metricSegmentDynamic = "semaphore_segment_dynamic"
	statusHealthy        = "healthy"
	statusStoreError     = "store_error"
	storeStateConnected  = "connected"
	storeStateConnecting = "connecting"
	storeStateError      = "error"
	storeStateIdle       = "idle"
	storeStateClosed     = "closed"
	resultHit            = "hit"
	resultMiss           = "miss"
	resultCoalesced      = "coalesced"
	resultShortCircuited = "short_circuited"
	resultUnknown        = "unknown"
	transitionOpen       = "open"
	transitionHalfOpen   = "half_open"
	transitionClosed     = "closed"
)

// SegmentCollector exposes the dynamic segments: what the resolvers did, the state of each store and the served
// segments by health. A scrape reads the status; it calls no store and evaluates no flag.
type SegmentCollector struct {
	src StatusSource

	lookups, calls, failed, breaker, stores, dynamic *prom.Desc
}

var _ prom.Collector = (*SegmentCollector)(nil)

// NewSegmentCollector returns the collector of the dynamic segments of src.
func NewSegmentCollector(src StatusSource) *SegmentCollector {
	desc := func(name, help string, labels ...string) *prom.Desc { return prom.NewDesc(name, help, labels, nil) }

	return &SegmentCollector{
		src:     src,
		lookups: desc(metricSegmentLookups, "Segment membership lookups by how the resolver answered them.", "result"),
		calls:   desc(metricSegmentCalls, "Calls of the resolvers to the stores."),
		failed:  desc(metricSegmentFailed, "Calls of the resolvers to the stores that failed."),
		breaker: desc(metricSegmentBreaker, "Transitions of the circuit breakers of the segments.", "to"),
		stores:  desc(metricSegmentStore, "1 for the state of each segment store connection.", "connection", "state"),
		dynamic: desc(metricSegmentDynamic, "Served dynamic segments by status.", "status"),
	}
}

// Describe implements [prom.Collector].
func (c *SegmentCollector) Describe(out chan<- *prom.Desc) {
	for _, d := range []*prom.Desc{c.lookups, c.calls, c.failed, c.breaker, c.stores, c.dynamic} {
		out <- d
	}
}

// Collect implements [prom.Collector].
func (c *SegmentCollector) Collect(out chan<- prom.Metric) {
	status := c.src.Status().Segments
	stats := status.Resolver

	counter := func(d *prom.Desc, v uint64, labels ...string) {
		out <- prom.MustNewConstMetric(d, prom.CounterValue, float64(v), labels...)
	}

	counter(c.lookups, stats.Hits, resultHit)
	counter(c.lookups, stats.Misses, resultMiss)
	counter(c.lookups, stats.Coalesced, resultCoalesced)
	counter(c.lookups, stats.ShortCircuited, resultShortCircuited)
	counter(c.lookups, stats.Unknown, resultUnknown)
	counter(c.calls, stats.Calls)
	counter(c.failed, stats.Failed)
	counter(c.breaker, stats.BreakerOpened, transitionOpen)
	counter(c.breaker, stats.BreakerHalfOpened, transitionHalfOpen)
	counter(c.breaker, stats.BreakerClosed, transitionClosed)

	for _, store := range status.Stores {
		states := []string{storeStateIdle, storeStateConnecting, storeStateConnected, storeStateError, storeStateClosed}

		for _, state := range states {
			value := 0.0
			if store.State == state {
				value = 1
			}

			out <- prom.MustNewConstMetric(c.stores, prom.GaugeValue, value, store.Name, state)
		}
	}

	var healthy, failing int
	for _, set := range status.Sets {
		healthy += set.Healthy
		failing += set.StoreError
	}

	out <- prom.MustNewConstMetric(c.dynamic, prom.GaugeValue, float64(healthy), statusHealthy)

	out <- prom.MustNewConstMetric(c.dynamic, prom.GaugeValue, float64(failing), statusStoreError)
}

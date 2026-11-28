package segmentobs

import (
	prom "github.com/prometheus/client_golang/prometheus"

	"github.com/blackbirdworks/semaphore/modules/core/segment/resolver"
)

// The series of the observers.
const (
	MetricLookups  = "semaphore_segment_lookups_total"
	MetricDuration = "semaphore_segment_lookup_duration_seconds"
	MetricUnknown  = "semaphore_segment_unknown_total"
	MetricNamed    = "semaphore_segment_named_lookups_total"
	MetricNamedUnk = "semaphore_segment_named_unknown_total"
	MetricOverflow = "semaphore_segment_named_overflow_total"
)

const labelConnection = "connection"

var _ prom.Collector = (*Observers)(nil)

type descs struct{ lookups, duration, unknown, named, namedUnk, overflow *prom.Desc }

func newDescs() descs {
	return descs{
		lookups: prom.NewDesc(MetricLookups,
			"Segment membership lookups by connection and how the resolver answered them (hit, miss and coalesced "+
				"are the cache outcomes).", []string{labelConnection, "result"}, nil),
		duration: prom.NewDesc(MetricDuration,
			"Duration of the calls of the resolvers of a connection (one call is one batch of lookups).",
			[]string{labelConnection}, nil),
		unknown: prom.NewDesc(MetricUnknown,
			"Lookups that stayed unknown by connection and reason (store, circuit_open, waiting, not_loaded, "+
				"oversized, invalid, unanswered, overdue).", []string{labelConnection, "reason"}, nil),
		named: prom.NewDesc(MetricNamed,
			"Segment lookups by segment name (opt-in, capped; names beyond the cap are in segment=\"other\").",
			[]string{"segment", "result"}, nil),
		namedUnk: prom.NewDesc(MetricNamedUnk,
			"Lookups that stayed unknown by segment name and reason (opt-in, capped).",
			[]string{"segment", "reason"}, nil),
		overflow: prom.NewDesc(MetricOverflow,
			"Observations of segments beyond the cap of names, counted in segment=\"other\".", nil, nil),
	}
}

// Describe implements [prom.Collector].
func (o *Observers) Describe(out chan<- *prom.Desc) {
	desc := newDescs()
	out <- desc.lookups

	out <- desc.duration

	out <- desc.unknown

	if o.named != nil {
		out <- desc.named

		out <- desc.namedUnk

		out <- desc.overflow
	}
}

// Collect implements [prom.Collector].
func (o *Observers) Collect(out chan<- prom.Metric) {
	desc := newDescs()
	counter := func(d *prom.Desc, v uint64, labels ...string) {
		out <- prom.MustNewConstMetric(d, prom.CounterValue, float64(v), labels...)
	}

	for _, read := range o.snapshot() {
		counter(desc.lookups, read.stats.Hits, read.name, "hit")
		counter(desc.lookups, read.stats.Misses, read.name, "miss")
		counter(desc.lookups, read.stats.Coalesced, read.name, "coalesced")
		counter(desc.lookups, read.stats.ShortCircuited, read.name, "short_circuited")
		counter(desc.lookups, read.stats.Unknown, read.name, "unknown")

		for i, n := range read.dist.Reasons {
			counter(desc.unknown, n, read.name, resolver.Reason(i).String())
		}

		sendHistogram(out, desc.duration, read)
	}

	if o.named == nil {
		return
	}

	for name, series := range o.named.snapshot() {
		counter(desc.named, series.results[namedHit].Load(), name, "hit")
		counter(desc.named, series.results[namedMiss].Load(), name, "miss")
		counter(desc.named, series.results[namedCoalesced].Load(), name, "coalesced")
		counter(desc.named, series.results[namedUnknown].Load(), name, "unknown")

		for i := range series.reasons {
			counter(desc.namedUnk, series.reasons[i].Load(), name, resolver.Reason(i).String())
		}
	}

	counter(desc.overflow, o.named.overflow.Load())
}

// sendHistogram sends the buckets of a connection as a cumulative Prometheus histogram.
func sendHistogram(out chan<- prom.Metric, desc *prom.Desc, read *reading) {
	bounds := resolver.DurationBounds()
	buckets := make(map[float64]uint64, len(bounds))

	var count uint64

	for i, n := range read.dist.DurationBuckets {
		count += n
		if i < len(bounds) {
			buckets[bounds[i].Seconds()] = count
		}
	}

	out <- prom.MustNewConstHistogram(desc, count, read.stats.Duration.Seconds(), buckets, read.name)
}

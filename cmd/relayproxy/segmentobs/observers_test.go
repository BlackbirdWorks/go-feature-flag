package segmentobs_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	prom "github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/segmentobs"
	"github.com/blackbirdworks/semaphore/modules/core/segment/resolver"
)

// gather reads every sample of the observers as "name{label=value,...}" -> value (the count for a histogram).
func gather(t *testing.T, obs *segmentobs.Observers) map[string]float64 {
	t.Helper()

	reg := prom.NewPedanticRegistry()
	require.NoError(t, reg.Register(obs))

	families, err := reg.Gather()
	require.NoError(t, err)

	out := map[string]float64{}

	for _, fam := range families {
		for _, sample := range fam.GetMetric() {
			labels := make([]string, 0, len(sample.GetLabel()))
			for _, label := range sample.GetLabel() {
				labels = append(labels, label.GetName()+"="+label.GetValue())
			}

			key := fam.GetName() + "{" + strings.Join(labels, ",") + "}"

			if fam.GetType() == dto.MetricType_HISTOGRAM {
				out[key] = float64(sample.GetHistogram().GetSampleCount())
			} else {
				out[key] = sample.GetCounter().GetValue()
			}
		}
	}

	return out
}

func TestObservers_PerConnectionSeries(t *testing.T) {
	t.Parallel()

	obs := segmentobs.New(segmentobs.Options{})
	audiences := obs.For("audiences")
	other := obs.For("other-store")

	audiences.ObserveCall(resolver.CallObservation{
		Lookups: 5, Hits: 2, Misses: 1, Coalesced: 1, ShortCircuited: 1, Unknown: 2, Duration: 3 * time.Millisecond,
	})
	audiences.ObserveSegment(resolver.SegmentObservation{
		Segment: "secret-segment-name", Unknown: 2, Failure: resolver.ReasonCircuitOpen,
	})
	other.ObserveCall(resolver.CallObservation{Lookups: 1, Hits: 1, Duration: 10 * time.Second})

	got := gather(t, obs)

	for key, want := range map[string]float64{
		"semaphore_segment_lookups_total{connection=audiences,result=hit}":             2,
		"semaphore_segment_lookups_total{connection=audiences,result=miss}":            1,
		"semaphore_segment_lookups_total{connection=audiences,result=coalesced}":       1,
		"semaphore_segment_lookups_total{connection=audiences,result=short_circuited}": 1,
		"semaphore_segment_lookups_total{connection=audiences,result=unknown}":         2,
		"semaphore_segment_lookups_total{connection=other-store,result=hit}":           1,
		"semaphore_segment_unknown_total{connection=audiences,reason=circuit_open}":    2,
		"semaphore_segment_unknown_total{connection=audiences,reason=store}":           0,
		"semaphore_segment_lookup_duration_seconds{connection=audiences}":              1,
		"semaphore_segment_lookup_duration_seconds{connection=other-store}":            1,
	} {
		assert.InDelta(t, want, got[key], 0, key)
		assert.Contains(t, got, key)
	}

	for key := range got {
		assert.NotContains(t, key, "secret-segment-name", "a segment name is never a default label")
		assert.NotContains(t, key, "segment=", "no per-segment series unless asked for")
	}

	stats := obs.Stats()
	assert.Equal(t, uint64(2), stats.Calls)
	assert.Equal(t, uint64(3), stats.Hits)
	assert.Equal(t, uint64(6), stats.Lookups)
}

func TestObservers_SameConnectionSameObserver(t *testing.T) {
	t.Parallel()

	obs := segmentobs.New(segmentobs.Options{})

	first := obs.For("a")

	assert.Same(t, first, obs.For("a"))
	assert.NotSame(t, first, obs.For("b"))
}

func TestObservers_DurationBuckets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantLe   map[float64]uint64
		name     string
		duration time.Duration
	}{
		{name: "fast", duration: 100 * time.Microsecond, wantLe: map[float64]uint64{0.0005: 1, 5: 1}},
		{name: "on a bound", duration: time.Millisecond, wantLe: map[float64]uint64{0.0005: 0, 0.001: 1, 5: 1}},
		{name: "slow", duration: 3 * time.Second, wantLe: map[float64]uint64{2.5: 0, 5: 1}},
		{name: "beyond the last bound", duration: time.Minute, wantLe: map[float64]uint64{5: 0}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			obs := segmentobs.New(segmentobs.Options{})
			obs.For("c").ObserveCall(resolver.CallObservation{Lookups: 1, Duration: tt.duration})

			reg := prom.NewPedanticRegistry()
			require.NoError(t, reg.Register(obs))

			families, err := reg.Gather()
			require.NoError(t, err)

			var hist *dto.Histogram

			for _, fam := range families {
				if fam.GetName() == segmentobs.MetricDuration {
					hist = fam.GetMetric()[0].GetHistogram()
				}
			}

			require.NotNil(t, hist)
			assert.Equal(t, uint64(1), hist.GetSampleCount())
			assert.InDelta(t, tt.duration.Seconds(), hist.GetSampleSum(), 1e-9)

			for _, b := range hist.GetBucket() {
				if want, ok := tt.wantLe[b.GetUpperBound()]; ok {
					assert.Equal(t, want, b.GetCumulativeCount(), "le %v", b.GetUpperBound())
				}
			}
		})
	}
}

func TestObservers_PerSegmentSeriesAreOptInAndCapped(t *testing.T) {
	t.Parallel()

	obs := segmentobs.New(segmentobs.Options{PerSegment: true, MaxSegments: 2})
	conn := obs.For("c")

	for _, name := range []string{"a", "b", "c", "d", "a"} {
		conn.ObserveSegment(
			resolver.SegmentObservation{Segment: name, Lookups: 2, Hits: 1, Unknown: 1, Failure: resolver.ReasonStore},
		)
	}

	got := gather(t, obs)

	for key, want := range map[string]float64{
		"semaphore_segment_named_lookups_total{result=hit,segment=a}":          2,
		"semaphore_segment_named_lookups_total{result=hit,segment=b}":          1,
		"semaphore_segment_named_lookups_total{result=hit,segment=other}":      2,
		"semaphore_segment_named_unknown_total{reason=store,segment=other}":    2,
		"semaphore_segment_named_unknown_total{reason=store,segment=a}":        2,
		"semaphore_segment_named_overflow_total{}":                             2,
		"semaphore_segment_unknown_total{connection=c,reason=store}":           5,
		"semaphore_segment_named_lookups_total{result=unknown,segment=b}":      1,
		"semaphore_segment_named_lookups_total{result=miss,segment=a}":         0,
		"semaphore_segment_named_lookups_total{result=coalesced,segment=a}":    0,
		"semaphore_segment_named_unknown_total{reason=circuit_open,segment=a}": 0,
	} {
		assert.Contains(t, got, key)
		assert.InDelta(t, want, got[key], 0, key)
	}

	for key := range got {
		assert.NotContains(t, key, "segment=c}", "a name beyond the cap has no series of its own")
		assert.NotContains(t, key, "segment=d}", "a name beyond the cap has no series of its own")
	}
}

func TestObservers_NameOtherSharesTheBucket(t *testing.T) {
	t.Parallel()

	obs := segmentobs.New(segmentobs.Options{PerSegment: true, MaxSegments: 1})
	conn := obs.For("c")
	conn.ObserveSegment(resolver.SegmentObservation{Segment: "other", Lookups: 1, Hits: 1})

	got := gather(t, obs)

	assert.InDelta(t, 1, got["semaphore_segment_named_lookups_total{result=hit,segment=other}"], 0)
	assert.InDelta(t, 0, got["semaphore_segment_named_overflow_total{}"], 0)
}

func TestObservers_DefaultCap(t *testing.T) {
	t.Parallel()

	obs := segmentobs.New(segmentobs.Options{PerSegment: true})
	conn := obs.For("c")

	for i := range segmentobs.DefaultMaxSegments + 7 {
		conn.ObserveSegment(resolver.SegmentObservation{Segment: fmt.Sprintf("s%d", i), Lookups: 1, Hits: 1})
	}

	got := gather(t, obs)

	assert.InDelta(t, 7, got["semaphore_segment_named_overflow_total{}"], 0)
	assert.InDelta(t, 7, got["semaphore_segment_named_lookups_total{result=hit,segment=other}"], 0)
}

func TestObservers_Concurrent(t *testing.T) {
	t.Parallel()

	obs := segmentobs.New(segmentobs.Options{PerSegment: true, MaxSegments: 3})
	conn := obs.For("c")
	done := make(chan struct{})

	const workers, rounds = 8, 200

	for w := range workers {
		go func() {
			defer func() { done <- struct{}{} }()

			for range rounds {
				conn.ObserveCall(resolver.CallObservation{Lookups: 1, Hits: 1, Duration: time.Millisecond})
				conn.ObserveSegment(resolver.SegmentObservation{Segment: fmt.Sprintf("s%d", w%5), Lookups: 1, Hits: 1})
			}
		}()
	}

	for range workers {
		<-done
	}

	got := gather(t, obs)

	assert.InDelta(t, workers*rounds, got["semaphore_segment_lookups_total{connection=c,result=hit}"], 0)
	assert.InDelta(t, workers*rounds, got["semaphore_segment_lookup_duration_seconds{connection=c}"], 0)
}

func TestObservers_Describe(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts segmentobs.Options
		want int
	}{
		{name: "default series", opts: segmentobs.Options{}, want: 3},
		{name: "with per-segment series", opts: segmentobs.Options{PerSegment: true}, want: 6},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ch := make(chan *prom.Desc, 16)
			segmentobs.New(tt.opts).Describe(ch)
			close(ch)

			assert.Len(t, ch, tt.want)
		})
	}
}

// BenchmarkObserveCall is the cost that a flag with dynamic segments adds on the evaluation path for the metrics:
// atomics only, no allocation, with and without the series by segment name.
func BenchmarkObserveCall(b *testing.B) {
	for _, perSegment := range []bool{false, true} {
		b.Run(fmt.Sprintf("perSegment=%v", perSegment), func(b *testing.B) {
			conn := segmentobs.New(segmentobs.Options{PerSegment: perSegment}).For("c")
			call := resolver.CallObservation{Lookups: 2, Hits: 1, Misses: 1, Duration: time.Millisecond}
			seg := resolver.SegmentObservation{Segment: "audience", Lookups: 2, Hits: 1, Misses: 1}

			b.ReportAllocs()

			for b.Loop() {
				conn.ObserveCall(call)
				conn.ObserveSegment(seg)
			}
		})
	}
}

package exporter_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thomaspoignant/go-feature-flag/exporter"
	"github.com/thomaspoignant/go-feature-flag/ffcontext"
	"github.com/thomaspoignant/go-feature-flag/internal/loadtest"
)

func benchEvent() exporter.FeatureEvent {
	return exporter.NewFeatureEvent(
		ffcontext.NewEvaluationContext("user-1"),
		"flag", true, "A", false, "v1", "SERVER", nil)
}

// resetEvery is the number of adds after which BenchmarkEventStoreAdd releases the events.
const resetEvery = 1_000_000

// resetStore consumes and releases every event of the store, so that the benchmark memory stays
// bounded whatever b.N is. It is cheap compared to the resetEvery adds it amortizes over.
func resetStore(b *testing.B, s exporter.EventStore[exporter.FeatureEvent]) {
	b.Helper()
	if err := s.ProcessPendingEvents("c", func(context.Context, []exporter.FeatureEvent) error {
		return nil
	}); err != nil {
		b.Error(err)
	}
	if c, ok := s.(interface{ CleanQueue() }); ok {
		c.CleanQueue()
	}
}

// BenchmarkEventStoreAdd measures Add on the event store with one consumer that only reads to
// reset the store: every 1M adds the pending events are consumed and released, to keep the
// benchmark memory bounded.
func BenchmarkEventStoreAdd(b *testing.B) {
	b.Run("serial", func(b *testing.B) {
		s := exporter.NewEventStore[exporter.FeatureEvent](time.Hour)
		defer s.Stop()
		s.AddConsumer("c")
		ev := benchEvent()
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			s.Add(ev)
			if (i+1)%resetEvery == 0 {
				resetStore(b, s)
			}
		}
	})
	b.Run("parallel", func(b *testing.B) {
		s := exporter.NewEventStore[exporter.FeatureEvent](time.Hour)
		defer s.Stop()
		s.AddConsumer("c")
		ev := benchEvent()
		b.ReportAllocs()
		b.ResetTimer()
		var added atomic.Int64
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				s.Add(ev)
				if added.Add(1)%resetEvery == 0 {
					resetStore(b, s)
				}
			}
		})
	})
}

// BenchmarkEventStoreFlushCycle measures one add-N-then-consume cycle (what a bulk exporter does
// every flush), with a periodic cleaning running every millisecond.
func BenchmarkEventStoreFlushCycle(b *testing.B) {
	for _, n := range []int{1000, 10000, 100000} {
		b.Run(fmt.Sprintf("events=%d", n), func(b *testing.B) {
			s := exporter.NewEventStore[exporter.FeatureEvent](time.Millisecond)
			defer s.Stop()
			s.AddConsumer("c")
			ev := benchEvent()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				for j := 0; j < n; j++ {
					s.Add(ev)
				}
				if err := s.ProcessPendingEvents("c", func(_ context.Context, _ []exporter.FeatureEvent) error {
					return nil
				}); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*n), "ns/event")
		})
	}
}

// BenchmarkManagerAddEvent measures Manager.AddEvent end to end (store + flush trigger) for the
// three exporter behaviours. It reports the events exported per AddEvent call ("exported/op") and
// the events that were not exported at the end of the run, either still retained in memory or
// dropped by the store limit ("retained/op", per AddEvent call): near 0 for a healthy exporter,
// close to 1 for a failing one.
func BenchmarkManagerAddEvent(b *testing.B) {
	for _, mode := range []loadtest.ExporterMode{
		loadtest.ExporterDiscard, loadtest.ExporterSlow, loadtest.ExporterFailing,
	} {
		for _, par := range []bool{false, true} {
			b.Run(fmt.Sprintf("%s/parallel=%t", mode, par), func(b *testing.B) {
				exp := loadtest.NewExporter(mode)
				exp.Delay = time.Millisecond
				m := exporter.NewManager[exporter.FeatureEvent](
					[]exporter.Config{{Exporter: exp, FlushInterval: 50 * time.Millisecond, MaxEventInMemory: 1000}},
					time.Millisecond, nil)
				m.Start()
				ev := benchEvent()
				b.ReportAllocs()
				b.ResetTimer()
				if par {
					b.RunParallel(func(pb *testing.PB) {
						for pb.Next() {
							m.AddEvent(ev)
						}
					})
				} else {
					for i := 0; i < b.N; i++ {
						m.AddEvent(ev)
					}
				}
				b.StopTimer()
				exported := exp.Events.Load()
				b.ReportMetric(float64(exported)/float64(b.N), "exported/op")
				b.ReportMetric(float64(max(int64(b.N)-exported, 0))/float64(b.N), "retained/op")
				b.ReportMetric(float64(exp.Failures.Load()), "failed-batches")
				m.Stop()
			})
		}
	}
}

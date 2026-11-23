package exporter_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/thomaspoignant/go-feature-flag/exporter"
	"github.com/thomaspoignant/go-feature-flag/ffcontext"
	"github.com/thomaspoignant/go-feature-flag/internal/loadtest"
)

func benchEvent(i int) exporter.FeatureEvent {
	return exporter.NewFeatureEvent(
		ffcontext.NewEvaluationContext(fmt.Sprintf("user-%d", i%1000)),
		"flag", true, "A", false, "v1", "SERVER", nil)
}

// BenchmarkEventStoreAdd measures Add on the event store with one consumer that never reads.
// The store is recreated every 1M adds to keep the benchmark memory bounded.
func BenchmarkEventStoreAdd(b *testing.B) {
	b.Run("serial", func(b *testing.B) {
		s := exporter.NewEventStore[exporter.FeatureEvent](time.Hour)
		defer s.Stop()
		s.AddConsumer("c")
		ev := benchEvent(1)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			s.Add(ev)
		}
	})
	b.Run("parallel", func(b *testing.B) {
		s := exporter.NewEventStore[exporter.FeatureEvent](time.Hour)
		defer s.Stop()
		s.AddConsumer("c")
		ev := benchEvent(1)
		b.ReportAllocs()
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				s.Add(ev)
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
			ev := benchEvent(1)
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
// three exporter behaviours. With a slow or failing exporter, it reports the number of events
// retained in memory at the end of the run ("retained/op" is events per AddEvent call).
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
				ev := benchEvent(1)
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
				b.ReportMetric(float64(exp.Events.Load())/float64(b.N), "exported/op")
				b.ReportMetric(float64(exp.Failures.Load()), "failed-batches")
				m.Stop()
			})
		}
	}
}

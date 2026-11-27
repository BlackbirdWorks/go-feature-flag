//go:build bench

package flagruntime_test

import (
	"context"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/ffcontext"
)

// BenchmarkScaleFullSwap measures Submit to swap for a generation that changes every flag set of the fleet, and
// reports the peak of the heap in use after a swap.
func BenchmarkScaleFullSwap(b *testing.B) {
	gens := [2]*flagruntime.Content{scaleGeneration(b, 1, scaleSets), scaleGeneration(b, 2, scaleSets)}
	step := engineBench(b, gens[0])

	var stats runtime.MemStats

	peak := uint64(0)
	gen := uint64(1)

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		gen++

		step(&flagruntime.Content{Generation: gen, Objects: gens[gen%2].Objects})

		runtime.ReadMemStats(&stats)
		peak = max(peak, stats.HeapInuse)
	}

	b.ReportMetric(float64(peak)/(1<<20), "peak-heap-MB")
}

// BenchmarkScaleReadersDuringSwaps evaluates through the evaluator from several goroutines while the engine swaps
// whole generations, and reports the p99 latency of an evaluation: a figure for the report, never asserted.
func BenchmarkScaleReadersDuringSwaps(b *testing.B) {
	const readers = 4

	gens := [2]*flagruntime.Content{scaleGeneration(b, 1, scaleSets), scaleGeneration(b, 2, scaleSets)}
	rounds := make(chan flagruntime.RoundResult, 1)
	holder := flagruntime.NewHolder()
	svc := newService(b)
	eng := flagruntime.NewEngine(nil, svc, holder, flagruntime.WithHooks(flagruntime.Hooks{
		AfterRound: func(res flagruntime.RoundResult) { rounds <- res },
	}))

	ctx, cancel := context.WithCancel(b.Context())
	done := make(chan struct{})

	go func() {
		defer close(done)

		eng.Run(ctx)
	}()

	go func() {
		for change := range eng.Changes() {
			_ = change // the consumer only drains
		}
	}()

	eng.Submit(gens[0])
	<-rounds

	eval := flagruntime.NewEvaluator(svc, nil)

	var (
		stop      atomic.Bool
		wg        sync.WaitGroup
		mu        sync.Mutex
		latencies []time.Duration
	)

	for reader := range readers {
		wg.Go(func() {
			evalCtx := ffcontext.NewEvaluationContext("user")

			var own []time.Duration

			for pos := reader; !stop.Load(); pos = (pos + 1) % scaleSets {
				set, _ := holder.Load().Route(scaleKey(pos/scaleSetsPerObject, pos%scaleSetsPerObject))

				started := time.Now()

				_, _ = eval.IntVariationDetails(set, "flag-000", evalCtx, -1)

				own = append(own, time.Since(started))
			}

			mu.Lock()

			latencies = append(latencies, own...)
			mu.Unlock()
		})
	}

	b.ReportAllocs()
	b.ResetTimer()

	gen := uint64(1)

	for b.Loop() {
		gen++

		eng.Submit(&flagruntime.Content{Generation: gen, Objects: gens[gen%2].Objects})
		<-rounds
	}

	b.StopTimer()
	stop.Store(true)
	wg.Wait()

	slices.Sort(latencies)

	if len(latencies) > 0 {
		b.ReportMetric(float64(latencies[len(latencies)*99/100].Nanoseconds()), "eval-p99-ns")
	}

	cancel()
	<-done
}

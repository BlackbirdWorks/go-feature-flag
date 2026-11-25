package loadtest

import (
	"context"
	"fmt"
	"io"
	"math/bits"
	"runtime"
	"runtime/metrics"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"text/tabwriter"
	"time"
)

const (
	// The latency histogram has subBuckets log scale sub-buckets per power of two.
	subBuckets    = 4
	subBucketBits = 2
	subBucketMask = subBuckets - 1
	// Defaults of the options of Run.
	defaultSampleInterval = 5 * time.Second
	defaultHeapMinGrowth  = 32 << 20
	defaultMaxHeap        = 6 << 30
	// Quantiles of the latency, and the unit conversions of the report.
	quantileMedian   = 0.50
	quantileP95      = 0.95
	quantileP99      = 0.99
	heapPollInterval = 200 * time.Millisecond
	nsPerUs          = 1e3
	mibShift         = 20
	mib              = 1 << mibShift
	// The verdict ignores the first warmupDivisor-th of the samples (at least minWarmupIntervals) and needs
	// minMeasuredSamples samples after them; halves is the number of windows the heap growth compares.
	minWarmupIntervals = 2
	warmupDivisor      = 5
	minMeasuredSamples = 3
	halves             = 2
	tabPadding         = 2
)

// Target is something we can evaluate flags against.
type Target interface {
	// Eval does one evaluation. i is a per-worker increasing counter.
	Eval(worker, i int) error
	// Extra returns target specific counters that are printed at the end of the run (optional).
	Extra() map[string]int64
	// Close releases the resources of the target.
	Close()
}

// Weighted is implemented by a target whose call evaluates several flags (a bulk request): the report
// then also gives flags evaluated per second.
type Weighted interface {
	// FlagsPerCall is the number of flags one successful call evaluates (at least 1).
	FlagsPerCall() int
}

// Options configure Run.
type Options struct {
	Name     string
	Duration time.Duration
	// Interval is the sampling period (default 5s).
	Interval time.Duration
	// Workers is the number of goroutines evaluating at max rate (default GOMAXPROCS).
	Workers int
	// WarmupIntervals is the number of leading samples ignored by the verdict
	// (default max(2, 20% of the samples)).
	WarmupIntervals int
	// HeapTolerance is the maximum relative growth of the median post-GC HeapInuse between the first and
	// the last half of the measured window (default 0.25 = 25%).
	HeapTolerance float64
	// HeapMinGrowthBytes is an absolute allowance under which growth never fails (default 32 MiB).
	HeapMinGrowthBytes uint64
	// GoroutineSlack is the number of goroutines the count may exceed its value at the end of the
	// warm-up by (default 64).
	GoroutineSlack int
	// MaxHeapBytes aborts the run (verdict FAIL) when the live heap exceeds it, to protect the
	// machine from an unbounded leak (default 6 GiB).
	MaxHeapBytes uint64
}

// Sample is one line of the time series.
type Sample struct {
	Elapsed     time.Duration
	EvalsPerSec float64
	Errors      int64
	P50, P99    time.Duration
	HeapAlloc   uint64 // after a forced GC
	HeapInuse   uint64 // after a forced GC
	HeapObjects uint64 // after a forced GC
	NumGC       uint32
	Goroutines  int
}

// Window summarizes the measured part of a run (the samples after the warm-up).
type Window struct {
	// Samples is the number of sampling intervals in the window.
	Samples int
	// CallsPerSec is the mean number of calls (requests) per second.
	CallsPerSec float64
	// FlagsPerCall is the number of flags evaluated by one call (1 unless the target is Weighted).
	FlagsPerCall int
	// FlagsPerSec is CallsPerSec * FlagsPerCall.
	FlagsPerSec float64
	// Errors is the number of failed calls in the window.
	Errors int64
	// P50, P95, P99 are latency quantiles of the sampled calls of the window (upper bound of the bucket).
	P50, P95, P99 time.Duration
}

// Report is the result of Run.
type Report struct {
	Extra   map[string]int64
	Name    string
	Samples []Sample
	Reasons []string
	// Window is the throughput and latency of the measured window; the zero value when it is too short.
	Window    Window
	TotalEval int64
	TotalErr  int64
	// HeapGrowthBytes is the growth of the median HeapInuse between the first and the last half of the measured window.
	HeapGrowthBytes float64
	Pass            bool
}

const (
	latSample  = 16 // 1 evaluation out of latSample is timed
	latBuckets = 64 * 4
)

type workerStats struct {
	count atomic.Int64
	errs  atomic.Int64
	hist  [latBuckets]atomic.Int64
	_     [64]byte
}

// bucket maps a duration to a log scale bucket with 4 sub-buckets per power of two.
func bucket(durNs int64) int {
	if durNs < 1 {
		durNs = 1
	}

	bitLen := bits.Len64(uint64(durNs)) - 1

	sub := 0
	if bitLen >= subBucketBits {
		sub = int((uint64(durNs) >> (bitLen - subBucketBits)) & subBucketMask)
	}

	return bitLen*subBuckets + sub
}

func bucketUpper(b int) time.Duration {
	l, sub := b/subBuckets, b%subBuckets
	if l < subBucketBits {
		return time.Duration(1 << (l + 1))
	}

	return time.Duration(int64(subBuckets+sub+1) << (l - subBucketBits))
}

func quantile(hist []int64, total int64, q float64) time.Duration {
	if total == 0 {
		return 0
	}

	target := int64(float64(total) * q)

	var cum int64
	for i, c := range hist {
		cum += c
		if cum > target {
			return bucketUpper(i)
		}
	}

	return bucketUpper(len(hist) - 1)
}

// Run evaluates the target at max rate with Workers goroutines for Duration and samples the
// throughput, the latency, the heap and the goroutine count every Interval. If out is not nil,
// each sample is written to it as it is taken.
func Run(ctx context.Context, t Target, opts Options, out io.Writer) Report {
	opts = applySoakDefaults(opts)

	ctx, cancel := context.WithTimeout(ctx, opts.Duration)
	defer cancel()

	var abortedAt atomic.Int64
	go watchdog(ctx, cancel, opts.MaxHeapBytes, &abortedAt)

	stats := make([]*workerStats, opts.Workers)

	var wg sync.WaitGroup

	for w := range opts.Workers {
		stats[w] = &workerStats{}

		st := stats[w]

		wg.Go(func() {
			evalLoop(ctx, t, w, st)
		})
	}

	rep := Report{Name: opts.Name}
	start := time.Now()
	last := start

	var lastCount, lastErr int64

	ticker := time.NewTicker(opts.Interval)
	defer ticker.Stop()

	hist := make([]int64, latBuckets)

	var sampleHists [][]int64

loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case <-ticker.C:
		}

		now := time.Now()
		count, errs, nLat := drain(stats, hist)
		sample := takeSample(now.Sub(start), now.Sub(last), count-lastCount, errs-lastErr, hist, nLat)
		rep.Samples = append(rep.Samples, sample)
		sampleHists = append(sampleHists, append([]int64(nil), hist...))

		if out != nil {
			if len(rep.Samples) == 1 {
				_, _ = fmt.Fprintln(out, header)
			}

			_, _ = fmt.Fprintln(out, sample.line())
		}

		last, lastCount, lastErr = now, count, errs
	}

	cancel()
	wg.Wait()

	for _, st := range stats {
		rep.TotalEval += st.count.Load()
		rep.TotalErr += st.errs.Load()
	}

	rep.Extra = t.Extra()
	judge(&rep, opts)

	rep.Window = summarize(rep.Samples, sampleHists, warmupCount(len(rep.Samples), opts), flagsPerCall(t))
	if a := abortedAt.Load(); a > 0 {
		rep.Pass = false
		rep.Reasons = append(rep.Reasons, fmt.Sprintf(
			"aborted after %s: live heap exceeded the %d MiB safety cap (unbounded growth)",
			time.Duration(a-1).Round(time.Millisecond), opts.MaxHeapBytes>>mibShift))
	}

	return rep
}

// applySoakDefaults fills zero-valued Options fields with default soak parameters.
func applySoakDefaults(opts Options) Options {
	if opts.Interval <= 0 {
		opts.Interval = defaultSampleInterval
	}

	if opts.Workers <= 0 {
		opts.Workers = runtime.GOMAXPROCS(0)
	}

	if opts.HeapTolerance <= 0 {
		opts.HeapTolerance = 0.25
	}

	if opts.HeapMinGrowthBytes == 0 {
		opts.HeapMinGrowthBytes = defaultHeapMinGrowth
	}

	if opts.GoroutineSlack == 0 {
		opts.GoroutineSlack = 64
	}

	if opts.MaxHeapBytes == 0 {
		opts.MaxHeapBytes = defaultMaxHeap
	}

	return opts
}

// takeSample forces a GC and builds the sample of the interval that just ended.
func takeSample(elapsed, interval time.Duration, calls, errs int64, hist []int64, nLat int64) Sample {
	runtime.GC()

	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	return Sample{
		Elapsed:     elapsed.Round(time.Second),
		EvalsPerSec: float64(calls) / interval.Seconds(),
		Errors:      errs,
		P50:         quantile(hist, nLat, quantileMedian),
		P99:         quantile(hist, nLat, quantileP99),
		HeapAlloc:   memStats.HeapAlloc,
		HeapInuse:   memStats.HeapInuse,
		HeapObjects: memStats.HeapObjects,
		NumGC:       memStats.NumGC,
		Goroutines:  runtime.NumGoroutine(),
	}
}

// watchdog cancels the run when the live heap exceeds maxHeap, recording when in abortedAt.
func watchdog(ctx context.Context, cancel context.CancelFunc, maxHeap uint64, abortedAt *atomic.Int64) {
	sample := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
	start := time.Now()

	ticker := time.NewTicker(heapPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			metrics.Read(sample)

			if sample[0].Value.Kind() == metrics.KindUint64 && sample[0].Value.Uint64() > maxHeap {
				abortedAt.Store(int64(time.Since(start)) + 1)
				cancel()

				return
			}
		}
	}
}

// evalLoop evaluates the target until ctx is done, sampling the latency of one call in latSample.
func evalLoop(ctx context.Context, t Target, w int, stats *workerStats) {
	for i := 0; ctx.Err() == nil; i++ {
		if i%latSample == 0 {
			start := time.Now()

			err := t.Eval(w, i)
			if err != nil {
				stats.errs.Add(1)
			}

			stats.hist[bucket(time.Since(start).Nanoseconds())].Add(1)
		} else {
			err := t.Eval(w, i)
			if err != nil {
				stats.errs.Add(1)
			}
		}

		stats.count.Add(1)
	}
}

// drain returns the cumulative counters of the workers and resets hist to the latency histogram
// accumulated since the previous call (nLat is its number of samples).
func drain(stats []*workerStats, hist []int64) (int64, int64, int64) {
	var count, errs, nLat int64

	for i := range hist {
		hist[i] = 0
	}

	for _, st := range stats {
		count += st.count.Load()

		errs += st.errs.Load()
		for i := range st.hist {
			c := st.hist[i].Swap(0)
			hist[i] += c
			nLat += c
		}
	}

	return count, errs, nLat
}

const header = "  t     evals/s   err   p50(us)  p99(us)  heapAlloc(MiB) heapInuse(MiB)  heapObjs  numGC  goroutines"

func (s Sample) line() string {
	return fmt.Sprintf("%4ds %10.0f %5d %9.1f %8.1f %14.1f %14.1f %9d %6d %11d",
		int(s.Elapsed.Seconds()), s.EvalsPerSec, s.Errors,
		float64(s.P50)/nsPerUs, float64(s.P99)/nsPerUs,
		float64(s.HeapAlloc)/mib, float64(s.HeapInuse)/mib, s.HeapObjects, s.NumGC, s.Goroutines)
}

// warmupCount is the number of leading samples ignored by the verdict and the window.
func warmupCount(n int, o Options) int {
	if o.WarmupIntervals > 0 {
		return o.WarmupIntervals
	}

	return max(minWarmupIntervals, n/warmupDivisor)
}

// flagsPerCall returns the number of flags one call of the target evaluates.
func flagsPerCall(t Target) int {
	if w, ok := t.(Weighted); ok {
		return max(1, w.FlagsPerCall())
	}

	return 1
}

// summarize computes the Window of the samples after warm. hists holds the latency histogram of each sample.
func summarize(samples []Sample, hists [][]int64, warm, perCall int) Window {
	if warm >= len(samples) {
		return Window{}
	}

	w := Window{Samples: len(samples) - warm, FlagsPerCall: perCall}
	merged := make([]int64, latBuckets)

	var total int64

	for i := warm; i < len(samples); i++ {
		w.CallsPerSec += samples[i].EvalsPerSec

		w.Errors += samples[i].Errors
		for b, c := range hists[i] {
			merged[b] += c
			total += c
		}
	}

	w.CallsPerSec /= float64(w.Samples)
	w.FlagsPerSec = w.CallsPerSec * float64(perCall)
	w.P50, w.P95, w.P99 = quantile(
		merged,
		total,
		quantileMedian,
	), quantile(
		merged,
		total,
		quantileP95,
	), quantile(
		merged,
		total,
		quantileP99,
	)

	return w
}

// judge computes the "memory stable" verdict.
//
// After the warm-up, the median post-GC HeapInuse of the last half of the samples is compared with
// the one of the first half; the run fails when it grew by more than both HeapTolerance of the
// first and HeapMinGrowthBytes. The median ignores the sawtooth of the events that wait for their
// flush (a sample taken just before a flush sees the whole backlog, one just after it sees none),
// which a peak or a single sample does not: a leak raises the median, a bounded backlog does not.
// It also fails when the goroutine count exceeds its value at the end of
// the warm-up by more than GoroutineSlack.
func judge(r *Report, opts Options) {
	r.Pass = true
	if r.TotalErr > 0 {
		r.Pass = false
		r.Reasons = append(r.Reasons, fmt.Sprintf("%d of %d evaluations failed", r.TotalErr, r.TotalEval))
	}

	sampleCount := len(r.Samples)

	warm := warmupCount(sampleCount, opts)
	if sampleCount-warm < minMeasuredSamples {
		r.Pass = false
		r.Reasons = append(r.Reasons, fmt.Sprintf(
			"insufficient samples: %d samples, %d warm-up, need at least 3 measured (increase -duration)",
			sampleCount,
			warm,
		))

		return
	}

	measured := r.Samples[warm:]
	// The post-GC heap of a healthy exporter is a sawtooth (the events wait for their flush), so
	// averages, peaks and fitted lines over a handful of samples are noisy: the flush interval and the
	// sampling interval beat against each other. We compare the median of the first half of the
	// measured window with the median of the last half: a leak raises the median, a bounded sawtooth
	// does not.
	half := max(1, len(measured)/halves)
	median := func(ss []Sample) float64 {
		heaps := make([]float64, len(ss))
		for i, s := range ss {
			heaps[i] = float64(s.HeapInuse)
		}

		slices.Sort(heaps)

		mid := len(heaps) / halves
		if len(heaps)%2 == 1 {
			return heaps[mid]
		}

		return (heaps[mid-1] + heaps[mid]) / halves
	}
	first, last := median(measured[:half]), median(measured[len(measured)-half:])
	r.HeapGrowthBytes = last - first

	allowed := max(opts.HeapTolerance*first, float64(opts.HeapMinGrowthBytes))
	if r.HeapGrowthBytes > allowed {
		r.Pass = false
		r.Reasons = append(r.Reasons, fmt.Sprintf(
			"median HeapInuse grows from %.1f MiB (first half) to %.1f MiB (last half), allowed +%.1f MiB",
			first/mib, last/mib, allowed/mib))
	}
	// the baseline is the last warm-up sample, so growth between it and the first measured
	// sample is counted.
	base := r.Samples[warm-1].Goroutines

	maxG := base
	for _, s := range measured {
		maxG = max(maxG, s.Goroutines)
	}

	if maxG > base+opts.GoroutineSlack {
		r.Pass = false
		r.Reasons = append(r.Reasons, fmt.Sprintf(
			"goroutines grow from %d to %d (allowed +%d)", base, maxG, opts.GoroutineSlack))
	}
}

// Summary writes the final verdict.
func (r Report) Summary(w io.Writer) {
	var avg float64
	for _, s := range r.Samples {
		avg += s.EvalsPerSec
	}

	if len(r.Samples) > 0 {
		avg /= float64(len(r.Samples))
	}

	verdict := "PASS"
	if !r.Pass {
		verdict = "FAIL"
	}

	_, _ = fmt.Fprintf(w,
		"\n%s: %s memory-stable (mean %.0f evals/s, %d evals, %d errors, heap growth over window %+.1f MiB)\n",
		r.Name, verdict, avg, r.TotalEval, r.TotalErr, r.HeapGrowthBytes/mib)
	for _, reason := range r.Reasons {
		_, _ = fmt.Fprintf(w, "  - %s\n", reason)
	}

	if r.Window.Samples > 0 {
		_, _ = fmt.Fprintf(w, "  window (%d samples after warm-up): %.0f calls/s, %.0f flags/s (%d per call), "+
			"%d errors, latency p50 %.1fus p95 %.1fus p99 %.1fus\n",
			r.Window.Samples, r.Window.CallsPerSec, r.Window.FlagsPerSec, r.Window.FlagsPerCall, r.Window.Errors,
			float64(r.Window.P50)/nsPerUs, float64(r.Window.P95)/nsPerUs, float64(r.Window.P99)/nsPerUs)
	}

	if len(r.Extra) > 0 {
		keys := make([]string, 0, len(r.Extra))
		for k := range r.Extra {
			keys = append(keys, k)
		}

		slices.Sort(keys)

		tabW := tabwriter.NewWriter(w, 0, 0, tabPadding, ' ', 0)

		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%s=%d", k, r.Extra[k]))
		}

		_, _ = fmt.Fprintln(tabW, "  target counters: "+strings.Join(parts, " "))
		_ = tabW.Flush()
	}
}

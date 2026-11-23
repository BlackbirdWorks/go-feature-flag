package loadtest

import (
	"context"
	"fmt"
	"io"
	"math/bits"
	"runtime"
	"runtime/metrics"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"text/tabwriter"
	"time"
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
	// HeapTolerance is the maximum relative growth of the post-GC peak HeapInuse between the first and
	// the last third of the measured window (default 0.25 = 25%).
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

// Report is the result of Run.
type Report struct {
	Name      string
	Samples   []Sample
	TotalEval int64
	TotalErr  int64
	Extra     map[string]int64
	Pass      bool
	Reasons   []string
	// HeapGrowthBytes is the growth of the peak HeapInuse between the first and the last third of the measured window.
	HeapGrowthBytes float64
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
func bucket(ns int64) int {
	if ns < 1 {
		ns = 1
	}
	l := bits.Len64(uint64(ns)) - 1
	sub := 0
	if l >= 2 {
		sub = int((uint64(ns) >> (l - 2)) & 3)
	}
	return l*4 + sub
}

func bucketUpper(b int) time.Duration {
	l, sub := b/4, b%4
	if l < 2 {
		return time.Duration(1 << (l + 1))
	}
	return time.Duration(int64(4+sub+1) << (l - 2))
}

func quantile(h []int64, total int64, q float64) time.Duration {
	if total == 0 {
		return 0
	}
	target := int64(float64(total) * q)
	var cum int64
	for i, c := range h {
		cum += c
		if cum > target {
			return bucketUpper(i)
		}
	}
	return bucketUpper(len(h) - 1)
}

// Run evaluates the target at max rate with Workers goroutines for Duration and samples the
// throughput, the latency, the heap and the goroutine count every Interval. If out is not nil,
// each sample is written to it as it is taken.
func Run(ctx context.Context, t Target, o Options, out io.Writer) Report {
	if o.Interval <= 0 {
		o.Interval = 5 * time.Second
	}
	if o.Workers <= 0 {
		o.Workers = runtime.GOMAXPROCS(0)
	}
	if o.HeapTolerance <= 0 {
		o.HeapTolerance = 0.25
	}
	if o.HeapMinGrowthBytes == 0 {
		o.HeapMinGrowthBytes = 32 << 20
	}
	if o.GoroutineSlack == 0 {
		o.GoroutineSlack = 64
	}
	if o.MaxHeapBytes == 0 {
		o.MaxHeapBytes = 6 << 30
	}
	ctx, cancel := context.WithTimeout(ctx, o.Duration)
	defer cancel()
	var abortedAt atomic.Int64
	go watchdog(ctx, cancel, o.MaxHeapBytes, &abortedAt)

	stats := make([]*workerStats, o.Workers)
	var wg sync.WaitGroup
	for w := 0; w < o.Workers; w++ {
		stats[w] = &workerStats{}
		wg.Add(1)
		go func(w int, st *workerStats) {
			defer wg.Done()
			evalLoop(ctx, t, w, st)
		}(w, stats[w])
	}

	rep := Report{Name: o.Name}
	start := time.Now()
	last := start
	var lastCount, lastErr int64
	ticker := time.NewTicker(o.Interval)
	defer ticker.Stop()
	hist := make([]int64, latBuckets)
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case <-ticker.C:
		}
		now := time.Now()
		count, errs, nLat := drain(stats, hist)
		runtime.GC()
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		s := Sample{
			Elapsed:     now.Sub(start).Round(time.Second),
			EvalsPerSec: float64(count-lastCount) / now.Sub(last).Seconds(),
			Errors:      errs - lastErr,
			P50:         quantile(hist, nLat, 0.50),
			P99:         quantile(hist, nLat, 0.99),
			HeapAlloc:   ms.HeapAlloc,
			HeapInuse:   ms.HeapInuse,
			HeapObjects: ms.HeapObjects,
			NumGC:       ms.NumGC,
			Goroutines:  runtime.NumGoroutine(),
		}
		rep.Samples = append(rep.Samples, s)
		if out != nil {
			if len(rep.Samples) == 1 {
				_, _ = fmt.Fprintln(out, header)
			}
			_, _ = fmt.Fprintln(out, s.line())
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
	judge(&rep, o)
	if a := abortedAt.Load(); a > 0 {
		rep.Pass = false
		rep.Reasons = append(rep.Reasons, fmt.Sprintf(
			"aborted after %s: live heap exceeded the %d MiB safety cap (unbounded growth)",
			time.Duration(a-1).Round(time.Millisecond), o.MaxHeapBytes>>20))
	}
	return rep
}

// watchdog cancels the run when the live heap exceeds maxHeap, recording when in abortedAt.
func watchdog(ctx context.Context, cancel context.CancelFunc, maxHeap uint64, abortedAt *atomic.Int64) {
	sample := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
	start := time.Now()
	tk := time.NewTicker(200 * time.Millisecond)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
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
func evalLoop(ctx context.Context, t Target, w int, st *workerStats) {
	for i := 0; ctx.Err() == nil; i++ {
		if i%latSample == 0 {
			start := time.Now()
			if err := t.Eval(w, i); err != nil {
				st.errs.Add(1)
			}
			st.hist[bucket(time.Since(start).Nanoseconds())].Add(1)
		} else if err := t.Eval(w, i); err != nil {
			st.errs.Add(1)
		}
		st.count.Add(1)
	}
}

// drain returns the cumulative counters of the workers and resets hist to the latency histogram
// accumulated since the previous call (nLat is its number of samples).
func drain(stats []*workerStats, hist []int64) (count, errs, nLat int64) {
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
		float64(s.P50)/1e3, float64(s.P99)/1e3,
		float64(s.HeapAlloc)/(1<<20), float64(s.HeapInuse)/(1<<20), s.HeapObjects, s.NumGC, s.Goroutines)
}

// judge computes the "memory stable" verdict.
//
// After the warm-up, the peak post-GC HeapInuse of the last third of the samples is compared with
// the one of the first third; the run fails when it grew by more than both HeapTolerance of the
// first and HeapMinGrowthBytes. It also fails when the goroutine count exceeds its value at the end of
// the warm-up by more than GoroutineSlack.
func judge(r *Report, o Options) {
	r.Pass = true
	n := len(r.Samples)
	warm := o.WarmupIntervals
	if warm <= 0 {
		warm = max(2, n/5)
	}
	if n-warm < 3 {
		r.Pass = false
		r.Reasons = append(r.Reasons, fmt.Sprintf(
			"insufficient samples: %d samples, %d warm-up, need at least 3 measured (increase -duration)", n, warm))
		return
	}
	m := r.Samples[warm:]
	// The post-GC heap of a healthy exporter is a sawtooth (the events wait for their flush), so
	// averages and fitted lines over a handful of samples are noisy. We compare the peak of the
	// first third of the measured window with the peak of the last third: a leak raises the
	// peaks, a bounded sawtooth does not.
	third := max(1, len(m)/3)
	peak := func(ss []Sample) float64 {
		var p uint64
		for _, s := range ss {
			p = max(p, s.HeapInuse)
		}
		return float64(p)
	}
	first, last := peak(m[:third]), peak(m[len(m)-third:])
	r.HeapGrowthBytes = last - first
	allowed := max(o.HeapTolerance*first, float64(o.HeapMinGrowthBytes))
	if r.HeapGrowthBytes > allowed {
		r.Pass = false
		r.Reasons = append(r.Reasons, fmt.Sprintf(
			"peak HeapInuse grows from %.1f MiB (first third) to %.1f MiB (last third), allowed +%.1f MiB",
			first/(1<<20), last/(1<<20), allowed/(1<<20)))
	}
	base := m[0].Goroutines
	maxG := base
	for _, s := range m {
		maxG = max(maxG, s.Goroutines)
	}
	if maxG > base+o.GoroutineSlack {
		r.Pass = false
		r.Reasons = append(r.Reasons, fmt.Sprintf(
			"goroutines grow from %d to %d (allowed +%d)", base, maxG, o.GoroutineSlack))
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
		r.Name, verdict, avg, r.TotalEval, r.TotalErr, r.HeapGrowthBytes/(1<<20))
	for _, reason := range r.Reasons {
		_, _ = fmt.Fprintf(w, "  - %s\n", reason)
	}
	if len(r.Extra) > 0 {
		keys := make([]string, 0, len(r.Extra))
		for k := range r.Extra {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%s=%d", k, r.Extra[k]))
		}
		_, _ = fmt.Fprintln(tw, "  target counters: "+strings.Join(parts, " "))
		_ = tw.Flush()
	}
}

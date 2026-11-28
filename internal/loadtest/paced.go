package loadtest

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"sync"
	"time"
)

// ErrPacedOptions is returned by [RunPaced] for options that cannot describe a run.
var ErrPacedOptions = errors.New("invalid paced run options")

// PacedOptions configure [RunPaced].
type PacedOptions struct {
	// Rate is the constant number of requests started per second (open loop: a slow answer does not delay the next
	// request, and a latency is counted from the instant the request was due, not from when a worker took it).
	Rate int
	// Duration is the length of the run.
	Duration time.Duration
	// Workers is the number of goroutines that send the requests.
	Workers int
	// Tail extends every window past the instant the disturbance returned: the effects of a swap (the collection of
	// the old generation) outlive the call that made it.
	Tail time.Duration
}

// Windows records the intervals of a run during which a disturbance (a reload) was in progress.
type Windows struct {
	start time.Time
	spans [][2]time.Duration
	open  time.Duration
	mu    sync.Mutex
}

// Begin marks the start of a disturbance.
func (w *Windows) Begin() {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.open = time.Since(w.start)
}

// End marks the end of the disturbance that Begin started.
func (w *Windows) End() {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.spans = append(w.spans, [2]time.Duration{w.open, time.Since(w.start)})
}

// Latency summarises the answers of one class of requests.
type Latency struct {
	Requests int
	Failures int
	P50      time.Duration
	P99      time.Duration
	Max      time.Duration
}

// PacedReport is the result of [RunPaced]: the requests split by whether they were due inside a window.
type PacedReport struct {
	Inside, Outside Latency
	// Windows is the number of disturbances, and WindowTime the total time covered by their windows (with the tail).
	Windows    int
	WindowTime time.Duration
	Elapsed    time.Duration
}

type pacedSample struct {
	due     time.Duration
	latency time.Duration
	failed  bool
}

// RunPaced sends opts.Rate requests per second to t for opts.Duration while disturb runs on its own goroutine (it
// brackets each disturbance with Begin and End on the windows it is given, and returns when ctx ends or it fails).
// It returns the latency of the requests due inside the windows and outside them. The latency is the real elapsed
// time: the measure is the output of the run.
func RunPaced(
	ctx context.Context, t Target, opts PacedOptions, disturb func(context.Context, *Windows) error,
) (PacedReport, error) {
	if opts.Rate <= 0 || opts.Duration <= 0 || opts.Workers <= 0 {
		return PacedReport{}, fmt.Errorf("%w: %+v", ErrPacedOptions, opts)
	}

	total := int(opts.Duration.Seconds() * float64(opts.Rate))
	gap := time.Second / time.Duration(opts.Rate)
	samples := make([]pacedSample, total)
	due := make(chan int, total)
	windows := &Windows{start: time.Now()}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		workers    sync.WaitGroup
		disturbErr error
		disturbed  sync.WaitGroup
	)

	disturbed.Go(func() { disturbErr = disturb(runCtx, windows) })

	for worker := range opts.Workers {
		workers.Go(func() {
			for i := range due {
				err := t.Eval(worker, i)
				latency := time.Since(windows.start) - samples[i].due
				samples[i].latency, samples[i].failed = latency, err != nil
			}
		})
	}

	for i := range total {
		samples[i].due = time.Duration(i) * gap

		if wait := samples[i].due - time.Since(windows.start); wait > 0 {
			select {
			case <-time.After(wait):
			case <-runCtx.Done():
			}
		}

		due <- i
	}

	close(due)
	workers.Wait()
	cancel()
	disturbed.Wait()

	if disturbErr != nil && !errors.Is(disturbErr, context.Canceled) {
		return PacedReport{}, fmt.Errorf("disturbance: %w", disturbErr)
	}

	return summarisePaced(samples, windows, opts.Tail, time.Since(windows.start)), nil
}

func summarisePaced(samples []pacedSample, windows *Windows, tail, elapsed time.Duration) PacedReport {
	windows.mu.Lock()
	defer windows.mu.Unlock()

	report := PacedReport{Windows: len(windows.spans), Elapsed: elapsed}

	for _, span := range windows.spans {
		report.WindowTime += span[1] + tail - span[0]
	}

	var inside, outside []pacedSample

	for _, sample := range samples {
		in := slices.ContainsFunc(windows.spans, func(span [2]time.Duration) bool {
			return sample.due >= span[0] && sample.due < span[1]+tail
		})
		if in {
			inside = append(inside, sample)
		} else {
			outside = append(outside, sample)
		}
	}

	report.Inside, report.Outside = latencyOf(inside), latencyOf(outside)

	return report
}

// latencyOf computes the exact quantiles (nearest rank) of the answers; a failed request counts as a failure and
// is left out of the quantiles.
func latencyOf(samples []pacedSample) Latency {
	out := Latency{Requests: len(samples)}
	lat := make([]time.Duration, 0, len(samples))

	for _, sample := range samples {
		if sample.failed {
			out.Failures++

			continue
		}

		lat = append(lat, sample.latency)
	}

	if len(lat) == 0 {
		return out
	}

	slices.Sort(lat)

	rank := func(q float64) time.Duration {
		return lat[min(len(lat)-1, max(0, int(math.Ceil(q*float64(len(lat))))-1))]
	}

	out.P50, out.P99, out.Max = rank(quantileMedian), rank(quantileP99), lat[len(lat)-1]

	return out
}

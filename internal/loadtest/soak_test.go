package loadtest

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/exporter"
)

func samples(heap []uint64, gor []int) []Sample {
	out := make([]Sample, len(heap))
	for i := range heap {
		out[i] = Sample{HeapInuse: heap[i], Goroutines: gor[i]}
	}

	return out
}

// sawtooth returns warm flat samples followed by n samples alternating between 100 and 10 MiB.
func sawtooth(warm, n int) []uint64 {
	const mib = 1 << 20
	out := slices.Repeat([]uint64{10 * mib}, warm)
	for i := range n {
		out = append(out, uint64(10+90*(i%2))*mib)
	}

	return out
}

func TestJudge(t *testing.T) {
	t.Parallel()
	const mib = 1 << 20
	flatG := []int{10, 10, 10, 10, 10, 10, 10, 10}
	tests := []struct {
		name       string
		heap       []uint64
		gor        []int
		opts       Options
		wantPass   bool
		wantReason string
	}{
		{
			name:     "stable",
			heap:     []uint64{50 * mib, 50 * mib, 50 * mib, 50 * mib, 50 * mib, 50 * mib, 50 * mib, 50 * mib},
			gor:      flatG,
			wantPass: true,
		},
		{
			name:       "heap leak",
			heap:       []uint64{50 * mib, 50 * mib, 100 * mib, 150 * mib, 200 * mib, 300 * mib, 400 * mib, 500 * mib},
			gor:        flatG,
			wantReason: "median HeapInuse grows",
		},
		{
			// the events wait for their flush: the heap of a healthy exporter oscillates, its median does not move
			name:     "bounded sawtooth",
			heap:     sawtooth(2, 12),
			gor:      slices.Repeat([]int{10}, 14),
			opts:     Options{WarmupIntervals: 2},
			wantPass: true,
		},
		{
			name:     "small heap growth under the absolute allowance",
			heap:     []uint64{1 * mib, 1 * mib, 1 * mib, 1 * mib, 1 * mib, 5 * mib, 5 * mib, 5 * mib},
			gor:      flatG,
			wantPass: true,
		},
		{
			name:       "goroutine growth during the measured window",
			heap:       []uint64{10 * mib, 10 * mib, 10 * mib, 10 * mib, 10 * mib, 10 * mib, 10 * mib, 10 * mib},
			gor:        []int{10, 10, 10, 10, 10, 10, 10, 200},
			wantReason: "goroutines grow from 10 to 200",
		},
		{
			// growth between the last warm-up sample and the first measured one must not be hidden
			name:       "goroutine jump right after the warm-up",
			heap:       []uint64{10 * mib, 10 * mib, 10 * mib, 10 * mib, 10 * mib, 10 * mib, 10 * mib, 10 * mib},
			gor:        []int{10, 10, 500, 500, 500, 500, 500, 500},
			wantReason: "goroutines grow from 10 to 500",
		},
		{
			name:       "not enough samples",
			heap:       []uint64{10 * mib, 10 * mib, 10 * mib},
			gor:        []int{1, 1, 1},
			wantReason: "insufficient samples",
		},
		{
			name:     "explicit warm-up",
			heap:     []uint64{10 * mib, 10 * mib, 10 * mib, 10 * mib, 10 * mib, 10 * mib},
			gor:      []int{10, 10, 10, 10, 10, 10},
			opts:     Options{WarmupIntervals: 3},
			wantPass: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			o := tt.opts
			o.HeapTolerance = 0.25
			o.HeapMinGrowthBytes = 32 << 20
			o.GoroutineSlack = 64
			if tt.name == "heap leak" {
				o.HeapMinGrowthBytes = 1
			}
			r := &Report{Samples: samples(tt.heap, tt.gor)}
			judge(r, o)
			assert.Equal(t, tt.wantPass, r.Pass, "reasons: %v", r.Reasons)
			if tt.wantReason != "" {
				require.Len(t, r.Reasons, 1)
				assert.Contains(t, r.Reasons[0], tt.wantReason)
			}
		})
	}
}

func TestJudge_EvaluationErrorsFail(t *testing.T) {
	t.Parallel()
	g := []int{10, 10, 10, 10, 10, 10}
	h := []uint64{1, 1, 1, 1, 1, 1}
	r := &Report{Samples: samples(h, g), TotalEval: 100, TotalErr: 7}
	judge(r, Options{HeapTolerance: 0.25, HeapMinGrowthBytes: 1 << 30, GoroutineSlack: 64})
	assert.False(t, r.Pass)
	require.Len(t, r.Reasons, 1)
	assert.Contains(t, r.Reasons[0], "7 of 100 evaluations failed")
}

func TestParseExporterMode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in      string
		want    ExporterMode
		wantErr bool
	}{
		{"discard", ExporterDiscard, false},
		{"slow", ExporterSlow, false},
		{"failing", ExporterFailing, false},
		{"", "", true},
		{"flaky", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			got, err := ParseExporterMode(tt.in)
			if tt.wantErr {
				require.Error(t, err)

				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestExporter_Export(t *testing.T) {
	t.Parallel()
	events := make([]exporter.ExportableEvent, 3)
	tests := []struct {
		name         string
		mode         ExporterMode
		wantErr      bool
		wantBatches  int64
		wantEvents   int64
		wantFailures int64
	}{
		{"discard", ExporterDiscard, false, 1, 3, 0},
		{"slow", ExporterSlow, false, 1, 3, 0},
		{"failing", ExporterFailing, true, 0, 0, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := NewExporter(tt.mode)
			e.Delay = time.Millisecond
			assert.True(t, e.IsBulk())
			err := e.Export(context.Background(), nil, events)
			assert.Equal(t, tt.wantErr, err != nil)
			assert.Equal(t, tt.wantBatches, e.Batches.Load())
			assert.Equal(t, tt.wantEvents, e.Events.Load())
			assert.Equal(t, tt.wantFailures, e.Failures.Load())
		})
	}

	t.Run("slow honours cancellation", func(t *testing.T) {
		t.Parallel()
		e := NewExporter(ExporterSlow)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		require.ErrorIs(t, e.Export(ctx, nil, events), context.Canceled)
		assert.Zero(t, e.Batches.Load())
	})
}

type fakeTarget struct {
	evals  atomic.Int64
	failEv int // every failEv-th evaluation fails (0 = never)
	closed atomic.Bool
}

func (f *fakeTarget) Eval(_, _ int) error {
	n := f.evals.Add(1)
	if f.failEv > 0 && n%int64(f.failEv) == 0 {
		return errors.New("boom")
	}
	time.Sleep(50 * time.Microsecond)

	return nil
}
func (f *fakeTarget) Extra() map[string]int64 { return map[string]int64{"evals": f.evals.Load()} }
func (f *fakeTarget) Close()                  { f.closed.Store(true) }

func TestRun_CountsEvaluationsAndErrors(t *testing.T) {
	t.Parallel()
	ft := &fakeTarget{failEv: 4}
	var out bytes.Buffer
	rep := Run(context.Background(), ft, Options{
		Name: "fake", Duration: 600 * time.Millisecond, Interval: 50 * time.Millisecond, Workers: 2,
		WarmupIntervals: 2, HeapMinGrowthBytes: 1 << 30,
	}, &out)
	assert.Equal(t, ft.evals.Load(), rep.TotalEval)
	assert.Positive(t, rep.TotalErr)
	assert.False(t, rep.Pass, "evaluation errors fail the verdict")
	assert.InDelta(t, float64(rep.TotalEval)/4, float64(rep.TotalErr), float64(rep.TotalEval)/4*0.2+4)
	assert.NotEmpty(t, rep.Samples)
	assert.Equal(t, rep.TotalEval, rep.Extra["evals"])
	assert.Contains(t, out.String(), "goroutines")
	assert.Positive(t, rep.Window.Samples, "the window covers the samples after the warm-up")
	assert.Equal(t, len(rep.Samples)-2, rep.Window.Samples)
	assert.Positive(t, rep.Window.CallsPerSec)
	assert.Equal(t, 1, rep.Window.FlagsPerCall)
	assert.Positive(t, rep.Window.P50)
	assert.GreaterOrEqual(t, rep.Window.P99, rep.Window.P50)

	var sum bytes.Buffer
	rep.Summary(&sum)
	assert.Contains(t, sum.String(), "fake")
	assert.Contains(t, sum.String(), "target counters: evals=")
}

func TestRun_StopsWhenContextIsCancelled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan Report, 1)
	go func() {
		done <- Run(ctx, &fakeTarget{}, Options{Duration: time.Hour, Interval: time.Hour, Workers: 2}, nil)
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case rep := <-done:
		assert.False(t, rep.Pass, "no measured sample: no verdict")
		assert.Positive(t, rep.TotalEval)
	case <-time.After(5 * time.Second):
		require.Fail(t, "Run did not stop after the context was cancelled")
	}
}

func TestRun_AbortsOnHeapCap(t *testing.T) {
	t.Parallel()
	rep := Run(context.Background(), &fakeTarget{}, Options{
		Duration: 5 * time.Second, Interval: time.Second, Workers: 1, MaxHeapBytes: 1,
	}, nil)
	assert.False(t, rep.Pass)
	var found bool
	for _, r := range rep.Reasons {
		found = found || strings.Contains(r, "safety cap")
	}
	assert.True(t, found, "reasons: %v", rep.Reasons)
}

func TestBucket(t *testing.T) {
	t.Parallel()
	prev := time.Duration(0)
	for ns := int64(1); ns < 1<<40; ns = ns*3/2 + 1 {
		b := bucket(ns)
		up := bucketUpper(b)
		assert.GreaterOrEqual(t, int64(up), ns, "ns=%d", ns)
		assert.GreaterOrEqual(t, up, prev)
		prev = up
	}
	assert.Equal(t, bucket(1), bucket(-5))
	assert.Equal(t, time.Duration(0), quantile(make([]int64, latBuckets), 0, 0.5))
}

func TestFlagKeyAndYAML(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "rule-bool-track", FlagKey("rule", "bool", true))
	assert.Equal(t, "rule-bool-notrack", FlagKey("rule", "bool", false))
	y := FlagsYAML()
	for _, s := range Scenarios() {
		for _, ty := range Types() {
			assert.Contains(t, y, FlagKey(s, ty, true)+":")
		}
	}
}

func TestLibraryTarget(t *testing.T) {
	t.Parallel()
	lt, err := NewLibraryTarget(LibraryOptions{
		FlagKey: "rule-bool-track", ExporterMode: ExporterDiscard, FlushInterval: 20 * time.Millisecond,
	})
	require.NoError(t, err)
	defer lt.Close()
	for i := range 50 {
		require.NoError(t, lt.Eval(0, i))
	}
	assert.Eventually(t, func() bool { return lt.Extra()["exported_events"] > 0 }, 5*time.Second, 20*time.Millisecond)
	assert.Contains(t, lt.Extra(), "dropped_events")
}

type weightedTarget struct{ n int }

func (weightedTarget) Eval(int, int) error     { return nil }
func (weightedTarget) Extra() map[string]int64 { return nil }
func (weightedTarget) Close()                  {}
func (w weightedTarget) FlagsPerCall() int     { return w.n }

func TestFlagsPerCall(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		target Target
		want   int
	}{
		{name: "plain target evaluates one flag", target: &fakeTarget{}, want: 1},
		{name: "weighted target", target: weightedTarget{n: 12}, want: 12},
		{name: "a weight below 1 counts as 1", target: weightedTarget{n: 0}, want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, flagsPerCall(tt.target))
		})
	}
}

func TestWarmupCount(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 2, warmupCount(5, Options{}), "at least two samples")
	assert.Equal(t, 6, warmupCount(30, Options{}), "20 percent of the samples")
	assert.Equal(t, 4, warmupCount(30, Options{WarmupIntervals: 4}), "explicit value wins")
}

func TestSummarize(t *testing.T) {
	t.Parallel()
	hist := func(durations ...time.Duration) []int64 {
		h := make([]int64, latBuckets)
		for _, d := range durations {
			h[bucket(d.Nanoseconds())]++
		}

		return h
	}
	us := time.Microsecond
	samples := []Sample{
		{EvalsPerSec: 1, Errors: 100}, // warm-up, ignored
		{EvalsPerSec: 1000, Errors: 1},
		{EvalsPerSec: 3000, Errors: 2},
	}
	hists := [][]int64{
		hist(time.Second), // warm-up, ignored
		hist(100*us, 100*us, 100*us, 100*us),
		hist(100*us, 100*us, 100*us, 100*us, 100*us, 100*us, 100*us, 100*us, 100*us, 10*time.Millisecond),
	}

	w := summarize(samples, hists, 1, 25)
	assert.Equal(t, 2, w.Samples)
	assert.InDelta(t, 2000, w.CallsPerSec, 1e-9)
	assert.InDelta(t, 50000, w.FlagsPerSec, 1e-9)
	assert.Equal(t, 25, w.FlagsPerCall)
	assert.Equal(t, int64(3), w.Errors, "the warm-up errors are not counted")
	assert.Less(t, w.P50, time.Millisecond)
	assert.GreaterOrEqual(t, w.P50, 100*us)
	assert.GreaterOrEqual(t, w.P99, 10*time.Millisecond, "the slow call shows in the tail")
	assert.LessOrEqual(t, w.P95, w.P99)
	assert.Less(t, w.P99, time.Second, "the warm-up latency is not counted")

	assert.Equal(t, Window{}, summarize(samples, hists, 3, 1), "no sample after the warm-up")
}

func TestSummaryPrintsTheWindow(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	Report{Name: "x", Pass: true, Window: Window{
		Samples: 4, CallsPerSec: 100, FlagsPerCall: 3, FlagsPerSec: 300, Errors: 2,
		P50: 100 * time.Microsecond, P95: 200 * time.Microsecond, P99: 300 * time.Microsecond,
	}}.Summary(&buf)
	out := buf.String()
	assert.Contains(t, out, "100 calls/s, 300 flags/s (3 per call)")
	assert.Contains(t, out, "2 errors")
	assert.Contains(t, out, "p95 200.0us")

	buf.Reset()
	Report{Name: "y", Pass: true}.Summary(&buf)
	assert.NotContains(t, buf.String(), "window (")
}

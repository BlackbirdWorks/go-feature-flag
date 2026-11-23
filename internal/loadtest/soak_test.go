package loadtest

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thomaspoignant/go-feature-flag/exporter"
)

func samples(heap []uint64, gor []int) []Sample {
	out := make([]Sample, len(heap))
	for i := range heap {
		out[i] = Sample{HeapInuse: heap[i], Goroutines: gor[i]}
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
			wantReason: "peak HeapInuse grows",
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
		t.Fatal("Run did not stop after the context was cancelled")
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
	for _, s := range Scenarios {
		for _, ty := range Types {
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
	for i := 0; i < 50; i++ {
		require.NoError(t, lt.Eval(0, i))
	}
	assert.Eventually(t, func() bool { return lt.Extra()["exported_events"] > 0 }, 5*time.Second, 20*time.Millisecond)
	assert.Contains(t, lt.Extra(), "dropped_events")
}

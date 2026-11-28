package soakapp

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/loadtest"
)

const (
	passVerdict        = "PASS memory-stable"
	failVerdict        = "FAIL memory-stable"
	ofrepBulkEndpoint  = "ofrep-bulk"
	h2cProtocol        = "h2c"
	discardExporter    = "discard"
	testWarmupMin      = 2
	testWarmupDivisor  = 5
	testMinMeasured    = 3
	testSamplesPass    = 4
	testSamplesShort   = 2
	testEvalsPerSample = 4
	testWorkers        = 2
	testHeapTolerance  = 100.0
	testSampleAlloc    = 1 << 20
	testSampleInuse    = 2 << 20
	testSampleObjs     = 100
	testSampleNumGC    = 1
	testSampleGors     = 4
	testCallsPerSec    = 100.0
	exitFail           = 1
)

// driveSamples returns a deterministic soakRunner that drives a fixed number of sampler ticks and
// evaluations against target without depending on wall-clock ticks, GC, or process memory metrics.
func driveSamples(sampleCount, evalsPerSample int, gotOpts *loadtest.Options) soakRunner {
	return func(
		ctx context.Context,
		target loadtest.Target,
		opts *loadtest.Options,
		_ io.Writer,
	) loadtest.Report {
		if gotOpts != nil {
			*gotOpts = *opts
		}

		if ctx.Err() != nil {
			return loadtest.Report{Name: opts.Name}
		}

		warm := opts.WarmupIntervals
		if warm <= 0 {
			warm = max(testWarmupMin, sampleCount/testWarmupDivisor)
		}

		samples := make([]loadtest.Sample, 0, sampleCount)

		var totalEval, totalErr, windowErr int64

		for sampleIdx := range sampleCount {
			var sampleErr int64

			for evalIdx := range evalsPerSample {
				worker := evalIdx % max(1, opts.Workers)

				err := target.Eval(worker, sampleIdx*evalsPerSample+evalIdx)
				if err != nil {
					sampleErr++
				}
			}

			totalEval += int64(evalsPerSample)
			totalErr += sampleErr

			if sampleIdx >= warm {
				windowErr += sampleErr
			}

			samples = append(samples, loadtest.Sample{
				Elapsed:     time.Duration(sampleIdx+1) * opts.Interval,
				EvalsPerSec: testCallsPerSec,
				Errors:      sampleErr,
				HeapAlloc:   testSampleAlloc,
				HeapInuse:   testSampleInuse,
				HeapObjects: testSampleObjs,
				NumGC:       testSampleNumGC,
				Goroutines:  testSampleGors,
			})
		}

		flagsPerCall := 1
		if weighted, ok := target.(loadtest.Weighted); ok && weighted.FlagsPerCall() > 0 {
			flagsPerCall = weighted.FlagsPerCall()
		}

		rep := loadtest.Report{
			Name:      opts.Name,
			Samples:   samples,
			TotalEval: totalEval,
			TotalErr:  totalErr,
			Extra:     target.Extra(),
			Pass:      true,
		}

		measured := sampleCount - warm
		if measured > 0 {
			rep.Window = loadtest.Window{
				Samples:      measured,
				CallsPerSec:  testCallsPerSec,
				FlagsPerCall: flagsPerCall,
				FlagsPerSec:  testCallsPerSec * float64(flagsPerCall),
				Errors:       windowErr,
			}
		}

		if totalErr > 0 {
			rep.Pass = false
			rep.Reasons = append(rep.Reasons, fmt.Sprintf("%d of %d evaluations failed", totalErr, totalEval))
		}

		if measured < testMinMeasured {
			rep.Pass = false
			rep.Reasons = append(rep.Reasons, fmt.Sprintf(
				"insufficient samples: %d samples, %d warm-up, need at least %d measured",
				sampleCount,
				warm,
				testMinMeasured,
			))
		}

		return rep
	}
}

func TestRun_Verdicts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantOpts   func(*testing.T, loadtest.Options)
		name       string
		args       []string
		wantStdout []string
		samples    int
		wantCode   int
	}{
		{
			name: "library target",
			args: []string{
				flagLongTarget, targetLibrary,
				"--duration", "1s",
				"--interval", "100ms",
				flagLongWarmup, "1",
				flagLongWorker, "2",
				"--heap-tolerance", "100",
			},
			samples:  testSamplesPass,
			wantCode: 0,
			wantStdout: []string{
				"# target=library exporter=discard workers=2",
				passVerdict,
				"window (3 samples after warm-up):",
			},
			wantOpts: func(t *testing.T, opts loadtest.Options) {
				t.Helper()
				assert.Equal(t, testWorkers, opts.Workers)
				assert.Equal(t, 1, opts.WarmupIntervals)
				assert.Equal(t, time.Second, opts.Duration)
				assert.Equal(t, 100*time.Millisecond, opts.Interval)
				assert.InDelta(t, testHeapTolerance, opts.HeapTolerance, 1e-9)
			},
		},
		{
			name: "library target default workers and legacy flags",
			args: []string{
				singleTarget, targetLibrary,
				"-duration", "600ms",
				"-interval", "100ms",
				"-warmup", "1",
				"-heap-tolerance", "100",
			},
			samples:    testSamplesPass,
			wantCode:   0,
			wantStdout: []string{"# target=library exporter=discard", passVerdict},
			wantOpts: func(t *testing.T, opts loadtest.Options) {
				t.Helper()
				assert.Equal(t, runtime.GOMAXPROCS(0), opts.Workers)
				assert.Equal(t, 600*time.Millisecond, opts.Duration)
				assert.Equal(t, 1, opts.WarmupIntervals)
				assert.InDelta(t, testHeapTolerance, opts.HeapTolerance, 1e-9)
			},
		},
		{
			name: "library target fail verdict on evaluation errors",
			args: []string{
				flagLongTarget, targetLibrary,
				flagLongFlag, "does-not-exist",
				flagLongWarmup, "1",
				flagLongWorker, "2",
			},
			samples:    testSamplesPass,
			wantCode:   exitFail,
			wantStdout: []string{failVerdict, "evaluations failed"},
		},
		{
			name: "library target fail verdict on insufficient samples",
			args: []string{
				flagLongTarget, targetLibrary,
				flagLongWarmup, "1",
				flagLongWorker, "2",
			},
			samples:    testSamplesShort,
			wantCode:   exitFail,
			wantStdout: []string{failVerdict, "insufficient samples"},
		},
		{
			name: "relay bulk endpoint over h2c",
			args: []string{
				flagLongTarget, targetRelay,
				"--endpoint", ofrepBulkEndpoint,
				"--protocol", h2cProtocol,
				"--exporter", discardExporter,
				flagLongWarmup, "1",
				flagLongWorker, "2",
			},
			samples:    testSamplesPass,
			wantCode:   0,
			wantStdout: []string{"endpoint=ofrep-bulk protocol=h2c", passVerdict, "flags/s"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var (
				stdout, stderr bytes.Buffer
				gotOpts        loadtest.Options
			)

			runner := driveSamples(tt.samples, testEvalsPerSample, &gotOpts)
			code := run(t.Context(), tt.args, &stdout, &stderr, runner)
			assert.Equal(t, tt.wantCode, code, "stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
			assert.Empty(t, stderr.String())

			for _, want := range tt.wantStdout {
				assert.Contains(t, stdout.String(), want)
			}

			if tt.wantOpts != nil {
				tt.wantOpts(t, gotOpts)
			}
		})
	}
}

// TestRun_CPUProfile is the only test that starts a CPU profile (there is one per process).
func TestRun_CPUProfile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "cpu.pprof")

	var stdout, stderr bytes.Buffer

	code := run(
		t.Context(),
		[]string{flagLongTarget, targetLibrary, "--cpuprofile", path, flagLongWarmup, "1", flagLongWorker, "2"},
		&stdout,
		&stderr,
		driveSamples(testSamplesPass, testEvalsPerSample, nil),
	)
	require.Equal(t, 0, code, stderr.String())
	assert.Contains(t, stdout.String(), passVerdict)

	st, err := os.Stat(path)
	require.NoError(t, err)
	assert.NotZero(t, st.Size())

	code = run(
		t.Context(),
		[]string{"--cpuprofile", filepath.Join(t.TempDir(), "missing", "cpu.pprof")},
		&stdout,
		&stderr,
		driveSamples(testSamplesPass, testEvalsPerSample, nil),
	)
	assert.Equal(t, exitUsage, code, "an unwritable profile path is a usage error")
}

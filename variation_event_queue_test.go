package ffclient_test

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ffclient "github.com/blackbirdworks/semaphore"
	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/retriever/fileretriever"
	"github.com/blackbirdworks/semaphore/utils/fflog"
)

var (
	errFailing = errors.New("failing")
)

type queueTestExporter struct {
	bulk   bool
	block  chan struct{} // when not nil, Export blocks until it is closed
	fail   bool
	events atomic.Int64
}

func (e *queueTestExporter) IsBulk() bool { return e.bulk }
func (e *queueTestExporter) Export(_ context.Context, _ *fflog.FFLogger, ev []exporter.ExportableEvent) error {
	if e.block != nil {
		<-e.block
	}
	if e.fail {
		return errFailing
	}
	e.events.Add(int64(len(ev)))

	return nil
}

// Test_EvaluationEventsAreQueuedNotSpawned checks that tracked evaluations are handed to a bounded
// queue drained by a small worker pool, instead of one goroutine per evaluation.
//
// runtime.NumGoroutine is process-wide and the other parallel tests of the package start clients,
// pollers and exporters, so it can only be used with a margin that dwarfs that noise (tens of
// goroutines). The exporter of the blocked cases never returns until the test releases it, so
// a goroutine-per-evaluation design would leave one parked goroutine per evaluation (thousands),
// while the queue design stays at the size of the worker pool whatever the number of evaluations.
// The growth must therefore stay below evals/goroutineRatio.
func Test_EvaluationEventsAreQueuedNotSpawned(t *testing.T) {
	t.Parallel()
	const flagFile = "testdata/flag-config.yaml"
	// goroutineRatio: the growth must be under 1/10 of the evaluations (>= 500 for 5000 evaluations),
	// far above the noise of the parallel tests and far below a goroutine per evaluation.
	const goroutineRatio = 10
	tests := []struct {
		name        string
		exp         func() *queueTestExporter
		queueSize   int
		evals       int
		maxQueued   int64
		wantAll     bool // every event must reach the exporter on Close
		wantDropped bool
	}{
		{
			name: "healthy exporter: nothing is lost, even when closing right away",
			exp:  func() *queueTestExporter { return &queueTestExporter{bulk: true} }, queueSize: 100000,
			evals: 5000, wantAll: true,
		},
		{
			name: "non bulk exporter: nothing is lost",
			exp:  func() *queueTestExporter { return &queueTestExporter{bulk: false} }, queueSize: 100000,
			evals: 500, wantAll: true,
		},
		{
			name: "blocked bulk exporter: events wait in the queue, no goroutine per evaluation",
			exp: func() *queueTestExporter {
				return &queueTestExporter{bulk: true, block: make(chan struct{})}
			}, queueSize: 100000,
			evals: 5000, wantAll: true,
		},
		{
			name: "blocked non bulk exporter: events wait in the queue, no goroutine per evaluation",
			exp: func() *queueTestExporter {
				return &queueTestExporter{bulk: false, block: make(chan struct{})}
			}, queueSize: 100000,
			evals: 5000, wantAll: true,
		},
		{
			name: "blocked failing exporter: no goroutine per evaluation, events are dropped and counted",
			exp: func() *queueTestExporter {
				return &queueTestExporter{bulk: true, fail: true, block: make(chan struct{})}
			}, queueSize: 100,
			evals: 20000, maxQueued: 200, wantDropped: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			exp := tt.exp()
			release := func() {
				if exp.block != nil {
					close(exp.block)
				}
			}
			before := runtime.NumGoroutine()
			c, err := ffclient.New(ffclient.Config{
				PollingInterval: time.Hour,
				Retriever:       &fileretriever.Retriever{Path: flagFile},
				EventQueueSize:  tt.queueSize,
				DataExporter: ffclient.DataExporter{
					Exporter: exp, FlushInterval: time.Hour, MaxEventInMemory: 50, MaxQueuedEvents: tt.maxQueued,
				},
			})
			require.NoError(t, err)

			var wg sync.WaitGroup
			var maxG atomic.Int64
			for range 4 {
				wg.Go(func() {
					for range tt.evals / 4 {
						_, _ = c.BoolVariation("test-flag", ffcontext.NewEvaluationContext("user"), false)
						if g := int64(runtime.NumGoroutine()); g > maxG.Load() {
							maxG.Store(g)
						}
					}
				})
			}
			// the evaluations must never wait for a blocked exporter.
			done := make(chan struct{})
			go func() { wg.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(time.Minute):
				release()
				require.Fail(t, "evaluations are blocked by the exporter")
			}
			if exp.block != nil {
				// the exporter is still blocked: parked goroutines are still alive.
				maxG.Store(max(maxG.Load(), int64(runtime.NumGoroutine())))
				assert.Less(t, int(maxG.Load())-before, tt.evals/goroutineRatio,
					"goroutines must not grow with the evaluations")
			}
			release()
			c.Close()
			if tt.wantAll {
				assert.Equal(t, int64(tt.evals), exp.events.Load())
				assert.Zero(t, c.DroppedEvents())
			}
			if tt.wantDropped {
				assert.Positive(t, c.DroppedEvents())
			}
		})
	}
}

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
	ffclient "github.com/thomaspoignant/go-feature-flag"
	"github.com/thomaspoignant/go-feature-flag/exporter"
	"github.com/thomaspoignant/go-feature-flag/ffcontext"
	"github.com/thomaspoignant/go-feature-flag/retriever/fileretriever"
	"github.com/thomaspoignant/go-feature-flag/utils/fflog"
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
		return errors.New("failing")
	}
	e.events.Add(int64(len(ev)))
	return nil
}

func Test_EvaluationEventsAreQueuedNotSpawned(t *testing.T) {
	t.Parallel()
	const flagFile = "testdata/flag-config.yaml"
	tests := []struct {
		name         string
		exp          func() *queueTestExporter
		queueSize    int
		evals        int
		maxQueued    int64
		wantAll      bool // every event must reach the exporter on Close
		wantDropped  bool
		maxGoroutine int
	}{
		{
			name: "healthy exporter: nothing is lost, even when closing right away",
			exp:  func() *queueTestExporter { return &queueTestExporter{bulk: true} }, queueSize: 100000,
			evals: 5000, wantAll: true, maxGoroutine: 30,
		},
		{
			name: "non bulk exporter: nothing is lost",
			exp:  func() *queueTestExporter { return &queueTestExporter{bulk: false} }, queueSize: 100000,
			evals: 500, wantAll: true, maxGoroutine: 30,
		},
		{
			name: "failing exporter: no goroutine per evaluation, events are dropped and counted",
			exp:  func() *queueTestExporter { return &queueTestExporter{bulk: true, fail: true} }, queueSize: 100,
			evals: 20000, maxQueued: 200, wantDropped: true, maxGoroutine: 30,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			exp := tt.exp()
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
			for w := 0; w < 4; w++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for i := 0; i < tt.evals/4; i++ {
						_, _ = c.BoolVariation("test-flag", ffcontext.NewEvaluationContext("user"), false)
						if g := int64(runtime.NumGoroutine()); g > maxG.Load() {
							maxG.Store(g)
						}
					}
				}()
			}
			wg.Wait()
			assert.LessOrEqual(t, int(maxG.Load())-before, tt.maxGoroutine, "goroutines must not grow with the evaluations")
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

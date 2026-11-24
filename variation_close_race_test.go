package ffclient_test

import (
	"context"
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

// gatedContext blocks the blockAt-th call of ExtractGOFFProtectedFields (0: never) until gate is
// closed. The last call of an evaluation is the one that builds its event, right before the event
// is handed to the exporters, which lets a test hold an evaluation at that point.
type gatedContext struct {
	ffcontext.Context
	calls   *atomic.Int64
	blockAt int64
	reached chan struct{}
	gate    chan struct{}
}

func (c gatedContext) ExtractGOFFProtectedFields() ffcontext.GoffContextSpecifics {
	if n := c.calls.Add(1); c.blockAt > 0 && n == c.blockAt {
		close(c.reached)
		<-c.gate
	}

	return c.Context.ExtractGOFFProtectedFields()
}

// finalFlushHookExporter runs onFinalFlush the first time it is called: it is the first exporter of
// the client and it only exports when the client is closed, after the event workers have exited.
type finalFlushHookExporter struct {
	once         sync.Once
	onFinalFlush func()
}

func (e *finalFlushHookExporter) IsBulk() bool { return true }
func (e *finalFlushHookExporter) Export(_ context.Context, _ *fflog.FFLogger, _ []exporter.ExportableEvent) error {
	e.once.Do(e.onFinalFlush)

	return nil
}

// An evaluation that is still running when the client is closed (a request that obtained a flag
// set that is removed) must not lose its event because the workers are gone: the event is handed to
// the exporters directly and exported by their final flush.
func Test_EvaluationFinishingWhileTheClientCloses(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		// bulk is the type of the exporter that flushes after the hook of the first one.
		bulk bool
	}{
		{name: "bulk exporter", bulk: true},
		{name: "live exporter", bulk: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			calls := &atomic.Int64{}
			newCtx := func(blockAt int64, reached, gate chan struct{}) ffcontext.Context {
				return gatedContext{
					Context: ffcontext.NewEvaluationContext("user"),
					calls:   calls, blockAt: blockAt, reached: reached, gate: gate,
				}
			}
			reached, gate := make(chan struct{}), make(chan struct{})
			lateDone := make(chan struct{})
			hook := &finalFlushHookExporter{}
			hook.onFinalFlush = func() {
				// the workers are gone: let the late evaluation finish and wait for it.
				close(gate)
				select {
				case <-lateDone:
				case <-time.After(time.Minute):
				}
			}
			second := &queueTestExporter{bulk: tt.bulk}
			c, err := ffclient.New(ffclient.Config{
				PollingInterval: time.Hour,
				Retriever:       &fileretriever.Retriever{Path: "testdata/flag-config.yaml"},
				DataExporters: []ffclient.DataExporter{
					{Exporter: hook, FlushInterval: time.Hour},
					{Exporter: second, FlushInterval: time.Hour},
				},
			})
			require.NoError(t, err)

			// 1. a first evaluation, to learn how many calls build an event, and to have something to flush.
			calls.Store(0)
			_, _ = c.BoolVariation("test-flag", newCtx(0, nil, nil), false)
			perEvaluation := calls.Load()
			require.Positive(t, perEvaluation)

			// 2. an evaluation held right before its event is handed to the exporters.
			calls.Store(0)
			go func() {
				defer close(lateDone)
				_, _ = c.BoolVariation("test-flag", newCtx(perEvaluation, reached, gate), false)
			}()
			select {
			case <-reached:
			case <-time.After(time.Minute):
				t.Fatal("the evaluation did not reach the event")
			}

			// 3. the client closes; its final flush releases the evaluation.
			c.Close()
			assert.Equal(t, int64(2), second.events.Load(), "the event of the evaluation that was running is exported")
			assert.Zero(t, c.DroppedEvents())
		})
	}
}

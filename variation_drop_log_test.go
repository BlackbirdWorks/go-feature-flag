package ffclient_test

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ffclient "github.com/blackbirdworks/semaphore"
	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/retriever/fileretriever"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
	"github.com/blackbirdworks/semaphore/testhelpers/slogutil"
)

// An evaluation never waits for the log sink, even the one that finds the evaluation queue full:
// the warning about the dropped events is written by a goroutine of the client.
func Test_EvaluationNeverWaitsForTheLogSinkWhenEventsAreDropped(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		queueSize  int
		evaluators int
		// wantProgress is whether every evaluator keeps evaluating while the log sink is blocked
		wantProgress bool
	}{
		{name: "queue of one event", queueSize: 1, evaluators: 4, wantProgress: true},
		{name: "queue of two events", queueSize: 2, evaluators: 8, wantProgress: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sink := slogutil.NewGatedHandler(slog.DiscardHandler, "dropped")
			defer sink.Release()

			client, err := ffclient.New(&ffclient.Config{
				PollingInterval: time.Hour,
				Retriever:       &fileretriever.Retriever{Path: fixtures.FlagConfigYAMLPath},
				LeveledLogger:   slog.New(sink),
				EventQueueSize:  tt.queueSize,
				DataExporter: ffclient.DataExporter{
					Exporter: &queueTestExporter{bulk: true}, FlushInterval: time.Hour, MaxEventInMemory: 50,
				},
			})
			require.NoError(t, err)

			counters := make([]atomic.Int64, tt.evaluators)
			stop := make(chan struct{})

			var wg sync.WaitGroup
			for i := range counters {
				wg.Go(func() {
					for {
						select {
						case <-stop:
							return
						default:
						}

						_, _ = client.BoolVariation(
							fixtures.FlagKeyTest,
							ffcontext.NewEvaluationContext(fixtures.ContextKindUser),
							false,
						)

						counters[i].Add(1)
					}
				})
			}

			defer func() {
				close(stop)
				sink.Release()
				wg.Wait()
				client.Close()
			}()

			require.Eventually(t, func() bool { return client.DroppedEvents() > 0 }, time.Minute, time.Millisecond,
				"the queue never overflowed")

			before := make([]int64, len(counters))
			for i := range counters {
				before[i] = counters[i].Load()
			}
			// every evaluator keeps evaluating while the sink is blocked.
			progressed := assert.Eventually(t, func() bool {
				for i := range counters {
					if counters[i].Load() < before[i]+1000 {
						return false
					}
				}

				return true
			}, time.Minute, time.Millisecond, "an evaluation is blocked by the log sink")
			assert.Equal(t, tt.wantProgress, progressed)
			// and the warning is written by someone else.
			select {
			case <-sink.Entered():
			case <-time.After(time.Minute):
				require.Fail(t, "the drop warning was not written")
			}
		})
	}
}

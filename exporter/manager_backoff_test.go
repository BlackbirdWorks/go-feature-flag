package exporter_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/utils/fflog"
)

// firstFailsExporter blocks its first Export until release is closed, then fails it; the following
// calls succeed. It records when each call starts and when the first one ended.
type firstFailsExporter struct {
	release chan struct{}
	started chan struct{}

	mu       sync.Mutex
	starts   []time.Time
	firstEnd time.Time
}

func (e *firstFailsExporter) IsBulk() bool { return true }
func (e *firstFailsExporter) Export(_ context.Context, _ *fflog.FFLogger, _ []exporter.ExportableEvent) error {
	e.mu.Lock()
	e.starts = append(e.starts, time.Now())
	n := len(e.starts)
	e.mu.Unlock()
	if n == 1 {
		close(e.started)
		<-e.release
		e.mu.Lock()
		e.firstEnd = time.Now()
		e.mu.Unlock()
		return assert.AnError
	}
	return nil
}

// A batch that arrives while an export is running must not make the worker retry a failed export
// right away: the retry of the event-count trigger waits for flushRetryBackoff (1s).
func Test_Manager_QueuedFlushRespectsBackoff(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		firstBatch int
		queued     int
	}{
		{name: "a second batch arrives during the failing export", firstBatch: 10, queued: 20},
		{name: "a small second batch arrives during the failing export", firstBatch: 10, queued: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			exp := &firstFailsExporter{release: make(chan struct{}), started: make(chan struct{})}
			m := exporter.NewManager[exporter.FeatureEvent]([]exporter.Config{{
				Exporter: exp, FlushInterval: time.Hour, MaxEventInMemory: 10, MaxQueuedEvents: 1000,
			}}, time.Hour, nil)
			am := m.(exporter.AsyncManager[exporter.FeatureEvent])

			am.QueueEvents(make([]exporter.FeatureEvent, tt.firstBatch))
			select {
			case <-exp.started:
			case <-time.After(time.Minute):
				t.Fatal("the export did not start")
			}
			// the export is blocked: the next batch asks for another flush
			am.QueueEvents(make([]exporter.FeatureEvent, tt.queued))
			close(exp.release)

			// keep feeding events until the exporter is called a second time.
			var second time.Time
			require.Eventually(t, func() bool {
				am.QueueEvents(make([]exporter.FeatureEvent, 10))
				exp.mu.Lock()
				defer exp.mu.Unlock()
				if len(exp.starts) >= 2 {
					second = exp.starts[1]
					return true
				}
				return false
			}, time.Minute, 10*time.Millisecond)

			exp.mu.Lock()
			gap := second.Sub(exp.firstEnd)
			exp.mu.Unlock()
			assert.GreaterOrEqual(t, gap, 900*time.Millisecond,
				"the retry of a failed export must wait for the backoff")
			m.Stop()
		})
	}
}

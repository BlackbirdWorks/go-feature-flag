package exporter_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/utils/fflog"
)

// gatedExporter counts what it exports; while gate is not nil its Export waits for it to be
// closed (started is closed when the first Export begins), and it fails when fail is set.
type gatedExporter struct {
	gate    chan struct{}
	started chan struct{}
	events  atomic.Int64
	once    sync.Once
	fail    bool
}

func (g *gatedExporter) IsBulk() bool { return true }
func (g *gatedExporter) Export(_ context.Context, _ *fflog.FFLogger, ev []exporter.ExportableEvent) error {
	if g.started != nil {
		g.once.Do(func() { close(g.started) })
	}
	if g.gate != nil {
		<-g.gate
	}
	if g.fail {
		return assert.AnError
	}
	g.events.Add(int64(len(ev)))

	return nil
}

// a failing or blocked exporter must not make the healthy ones lose events, and the memory stays
// bounded (the events the broken exporter can not take are the ones dropped).
func Test_Manager_BrokenExporterDoesNotStarveHealthyOne(t *testing.T) {
	t.Parallel()
	tests := []struct {
		broken func() *gatedExporter
		name   string
		// wantHealthy is the number of events the healthy exporter receives, wantDropping tells that the broken
		// one loses events once its backlog is full
		wantHealthy  int64
		async        bool
		wantDropping bool
	}{
		{
			name:        "failing exporter, AddEvent",
			broken:      func() *gatedExporter { return &gatedExporter{fail: true} },
			wantHealthy: 500, wantDropping: true,
		},
		{
			name:        "failing exporter, QueueEvents",
			async:       true,
			broken:      func() *gatedExporter { return &gatedExporter{fail: true} },
			wantHealthy: 500, wantDropping: true,
		},
		{
			name: "blocked exporter, QueueEvents", async: true,
			broken: func() *gatedExporter {
				return &gatedExporter{gate: make(chan struct{}), started: make(chan struct{})}
			},
			wantHealthy: 500, wantDropping: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			const (
				queueLimit = 50
				perBatch   = 5
				batches    = 100
			)
			broken, healthy := tt.broken(), &gatedExporter{}
			m := exporter.NewManager[exporter.FeatureEvent]([]exporter.Config{
				{Exporter: broken, FlushInterval: time.Hour, MaxEventInMemory: perBatch, MaxQueuedEvents: queueLimit},
				{Exporter: healthy, FlushInterval: time.Hour, MaxEventInMemory: perBatch, MaxQueuedEvents: queueLimit},
			}, time.Hour, nil)
			am, ok := m.(exporter.AsyncManager[exporter.FeatureEvent])
			require.True(t, ok)
			released := false
			release := func() {
				if broken.gate != nil && !released {
					released = true
					close(broken.gate)
				}
			}
			defer release()
			for b := 1; b <= batches; b++ {
				if tt.async {
					am.QueueEvents(make([]exporter.FeatureEvent, perBatch))
					// the healthy exporter keeps up: wait for it so that the test does not
					// depend on the speed of the background flushes.
					require.Eventually(t, func() bool { return healthy.events.Load() == int64(b*perBatch) },
						10*time.Second, time.Millisecond, "batch %d", b)
				} else {
					for range perBatch {
						m.AddEvent(exporter.FeatureEvent{})
					}
				}
			}
			release()
			m.Stop()
			assert.Equal(t, tt.wantHealthy, healthy.events.Load(), "the healthy exporter receives everything")
			dc, ok := m.(exporter.DroppedEventsCounter)
			require.True(t, ok)
			assert.Equal(t, tt.wantDropping, dc.DroppedEvents() > 0,
				"the broken exporter loses events once its backlog is full, and they are counted")
		})
	}
}

// the flush threshold must be reachable: MaxQueuedEvents below MaxEventInMemory must not turn a
// healthy exporter into one that only exports on the timer.
func Test_Manager_QueueLimitBelowFlushThreshold(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		maxMem, maxQ int64
		events       int
		wantExported int64
	}{
		{name: "limit far below the threshold", maxMem: 1000, maxQ: 10, events: 100, wantExported: 100},
		{name: "limit one below the threshold", maxMem: 10, maxQ: 9, events: 100, wantExported: 99},
		{name: "limit above the threshold", maxMem: 10, maxQ: 100, events: 100, wantExported: 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			exp := &gatedExporter{}
			m := exporter.NewManager[exporter.FeatureEvent]([]exporter.Config{{
				Exporter: exp, FlushInterval: time.Hour, MaxEventInMemory: tt.maxMem, MaxQueuedEvents: tt.maxQ,
			}}, time.Hour, nil)
			for range tt.events {
				m.AddEvent(exporter.FeatureEvent{})
			}
			// no Stop yet: nothing but the event count can have triggered the flushes.
			assert.Equal(t, tt.wantExported, exp.events.Load())
			dc, ok := m.(exporter.DroppedEventsCounter)
			require.True(t, ok)
			assert.Zero(t, dc.DroppedEvents())
			m.Stop()
		})
	}
}

// a successful flush (held open while other events arrive) must not be taken for a failure. The test runs on the
// fake time of a synctest bubble: once the workers are idle, no time has passed, so a backoff scheduled by the
// successful flush (one second) would still be running and the third batch would not have been exported.
func Test_Manager_SuccessfulFlushDoesNotBackOff(t *testing.T) {
	t.Parallel()
	const threshold = 10
	synctest.Test(t, func(t *testing.T) {
		exp := &gatedExporter{gate: make(chan struct{}), started: make(chan struct{})}
		m := exporter.NewManager[exporter.FeatureEvent]([]exporter.Config{{
			Exporter: exp, FlushInterval: time.Hour, MaxEventInMemory: threshold,
		}}, time.Hour, nil)
		defer m.Stop() // also when an assertion fails: the bubble waits for the workers
		am, ok := m.(exporter.AsyncManager[exporter.FeatureEvent])
		require.True(t, ok)
		am.QueueEvents(make([]exporter.FeatureEvent, threshold))
		<-exp.started
		am.QueueEvents(make([]exporter.FeatureEvent, threshold)) // arrives while the first flush runs
		close(exp.gate)
		synctest.Wait()
		require.Equal(t, int64(2*threshold), exp.events.Load())
		// a third batch must be flushed by the event count, not wait for the timer (1h) or a backoff.
		am.QueueEvents(make([]exporter.FeatureEvent, threshold))
		synctest.Wait()
		assert.Equal(t, int64(3*threshold), exp.events.Load())
	})
}

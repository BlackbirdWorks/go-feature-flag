package exporter_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thomaspoignant/go-feature-flag/exporter"
	"github.com/thomaspoignant/go-feature-flag/utils/fflog"
)

// gatedExporter counts what it exports; while gate is not nil its Export waits for it to be
// closed (started is closed when the first Export begins), and it fails when fail is set.
type gatedExporter struct {
	events  atomic.Int64
	fail    bool
	gate    chan struct{}
	started chan struct{}
	once    sync.Once
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
		name   string
		broken func() *gatedExporter
		async  bool
	}{
		{name: "failing exporter, AddEvent", broken: func() *gatedExporter { return &gatedExporter{fail: true} }},
		{name: "failing exporter, QueueEvents", async: true, broken: func() *gatedExporter { return &gatedExporter{fail: true} }},
		{name: "blocked exporter, QueueEvents", async: true, broken: func() *gatedExporter {
			return &gatedExporter{gate: make(chan struct{}), started: make(chan struct{})}
		}},
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
			am := m.(exporter.AsyncManager[exporter.FeatureEvent])
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
					for i := 0; i < perBatch; i++ {
						m.AddEvent(exporter.FeatureEvent{})
					}
				}
			}
			release()
			m.Stop()
			assert.Equal(t, int64(batches*perBatch), healthy.events.Load(), "the healthy exporter receives everything")
			assert.Positive(t, m.(exporter.DroppedEventsCounter).DroppedEvents(),
				"the broken exporter loses events once its backlog is full, and they are counted")
		})
	}
}

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

// a successful flush (held open while other events arrive) must not be taken for a failure.
func Test_Manager_SuccessfulFlushDoesNotBackOff(t *testing.T) {
	t.Parallel()
	const threshold = 10
	exp := &gatedExporter{gate: make(chan struct{}), started: make(chan struct{})}
	m := exporter.NewManager[exporter.FeatureEvent]([]exporter.Config{{
		Exporter: exp, FlushInterval: time.Hour, MaxEventInMemory: threshold,
	}}, time.Hour, nil)
	am := m.(exporter.AsyncManager[exporter.FeatureEvent])
	am.QueueEvents(make([]exporter.FeatureEvent, threshold))
	<-exp.started
	am.QueueEvents(make([]exporter.FeatureEvent, threshold)) // arrives while the first flush runs
	close(exp.gate)
	require.Eventually(t, func() bool { return exp.events.Load() == 2*threshold }, 10*time.Second, time.Millisecond)
	// a third batch must be flushed by the event count, not wait for the timer (1h) or a backoff.
	am.QueueEvents(make([]exporter.FeatureEvent, threshold))
	assert.Eventually(t, func() bool { return exp.events.Load() == 3*threshold }, 500*time.Millisecond, time.Millisecond)
	m.Stop()
}

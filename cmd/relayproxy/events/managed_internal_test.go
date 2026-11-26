package events

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/exporter"
)

// plainManager is a manager without QueueEvents and without a drop counter: the minimum of [FeatureManager].
type plainManager struct {
	added []Event
	mu    sync.Mutex
}

func (p *plainManager) AddEvent(event Event) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.added = append(p.added, event)
}

func (*plainManager) Start() {}
func (*plainManager) Stop()  {}

// asyncManager adds QueueEvents and DroppedEvents.
type asyncManager struct {
	batches [][]Event
	mu      sync.Mutex
	dropped int64
}

func (*asyncManager) AddEvent(Event) {}
func (*asyncManager) Start()         {}
func (*asyncManager) Stop()          {}

func (a *asyncManager) QueueEvents(batch []Event) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.batches = append(a.batches, append([]Event(nil), batch...))
}

func (a *asyncManager) DroppedEvents() int64 { return a.dropped }

// TestNewManaged checks how an instance hands events to its manager: in batches when the manager has
// QueueEvents, one by one with AddEvent when it has not, and how it reads the drops of the manager.
func TestNewManaged(t *testing.T) {
	t.Parallel()

	tests := []struct {
		manager     func() FeatureManager
		name        string
		wantBatches int
		wantAdded   int
		wantDropped uint64
	}{
		{
			name:    "a manager with QueueEvents gets the events in batches",
			manager: func() FeatureManager { return &asyncManager{dropped: 3} }, wantBatches: 1, wantDropped: 3,
		},
		{
			name:        "a manager without QueueEvents gets them one by one",
			manager:     func() FeatureManager { return &plainManager{} },
			wantAdded:   3,
			wantDropped: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			def := NewExporterDef("a", "file", "v1", echoExporter{}, exporter.Config{})
			entry := &nameEntry{name: "a"}
			def.wrapper().name = &entry.stats

			mgr := tt.manager()
			// the batch size is 8 and three events are waiting when the goroutine of the hand-off starts
			inst := newManaged(&def, mgr, entry, 8, 8)
			entry.latest.Store(inst)

			inst.offer([]Event{{Key: "1"}, {Key: "2"}, {Key: "3"}})

			var background sync.WaitGroup

			inst.startClose(&background)
			background.Wait()

			switch typed := mgr.(type) {
			case *asyncManager:
				got := 0
				for _, batch := range typed.batches {
					got += len(batch)
				}

				assert.Equal(t, 3, got)
				assert.LessOrEqual(t, len(typed.batches), 3)
				assert.GreaterOrEqual(t, len(typed.batches), tt.wantBatches)
			case *plainManager:
				assert.Len(t, typed.added, tt.wantAdded)
			}

			assert.Equal(t, tt.wantDropped, inst.managerDropped())
			assert.True(t, inst.isFinished())
			assert.Equal(t, uint64(3), entry.stats.handed.Load())
		})
	}
}

// TestWaitCollectors checks that the shutdown waits for a Collect that is between its check and its send, and
// stops waiting when its deadline passes.
func TestWaitCollectors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		inflight     int64
		release      bool
		cancelBefore bool
		wantInflight int64
	}{
		{name: "nothing in flight returns at once", inflight: 0, wantInflight: 0},
		{name: "waits until the collector is done", inflight: 1, release: true, wantInflight: 0},
		{name: "a passed deadline stops the wait", inflight: 1, cancelBefore: true, wantInflight: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			disp := NewDispatcher(&DispatcherConfig{})
			disp.inflight.Store(tt.inflight)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			if tt.cancelBefore {
				cancel()
			}

			done := make(chan struct{})

			go func() {
				disp.waitCollectors(ctx)
				close(done)
			}()

			if tt.release {
				disp.inflight.Add(-1)
			}

			<-done

			assert.Equal(t, tt.wantInflight, disp.inflight.Load())
		})
	}
}

// TestSealedPrintsNothing checks the sealed holder on the values it can hold.
func TestSealedPrintsNothing(t *testing.T) {
	t.Parallel()

	sealed := Seal("a-secret-url")

	require.Equal(t, "a-secret-url", sealed.Unseal())
	assert.Equal(t, "[REDACTED]", sealed.String())
	assert.Equal(t, "[REDACTED]", sealed.GoString())
	assert.Equal(t, "[REDACTED]", sealed.LogValue().String())
}

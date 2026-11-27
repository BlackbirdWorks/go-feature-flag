package events

import (
	"context"
	"log/slog"
	"slices"
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

// managedCase is a row of [TestNewManaged].
type managedCase struct {
	manager     FeatureManager
	received    func() []string
	name        string
	wantKeys    []string
	wantDropped uint64
}

// TestNewManaged checks how an instance hands events to its manager: in batches when the manager has
// QueueEvents, one by one with AddEvent when it has not, and how it reads the drops of the manager.
func TestNewManaged(t *testing.T) {
	t.Parallel()

	async, plain := &asyncManager{dropped: 3}, &plainManager{}

	tests := []managedCase{
		{
			name:        "a manager with QueueEvents gets the events in batches",
			manager:     async,
			received:    func() []string { return batchKeys(async.batches) },
			wantKeys:    []string{"1", "2", "3"},
			wantDropped: 3,
		},
		{
			name: "a manager without QueueEvents gets them one by one", manager: plain,
			received: func() []string { return batchKeys([][]Event{plain.added}) }, wantKeys: []string{"1", "2", "3"},
			wantDropped: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			def := NewExporterDef("a", "file", "v1", echoExporter{}, exporter.Config{})
			entry := &nameEntry{name: "a"}
			def.wrapper().name = &entry.stats

			inst := newManaged(&def, tt.manager, entry, 8, 8, slog.New(slog.DiscardHandler))
			entry.latest.Store(inst)

			inst.offer([]Event{{Key: "1"}, {Key: "2"}, {Key: "3"}})

			inst.startClose(context.Background())
			<-inst.finished

			assert.Equal(t, tt.wantKeys, tt.received())
			assert.Equal(t, tt.wantDropped, inst.managerDropped())
			assert.True(t, inst.isFinished())
			assert.Equal(t, uint64(3), entry.stats.handed.Load())
		})
	}
}

// batchKeys returns the sorted keys of the events of the batches.
func batchKeys(batches [][]Event) []string {
	var out []string

	for _, batch := range batches {
		for _, event := range batch {
			out = append(out, event.Key)
		}
	}

	slices.Sort(out)

	return out
}

// TestWaitCollectors checks that the shutdown waits for a Collect that is between its check and its send, and
// stops waiting when its deadline passes.
func TestWaitCollectors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		// act runs while waitCollectors waits (or before it starts, for a context that is done)
		act          func(disp *Dispatcher, cancel context.CancelFunc)
		name         string
		inflight     int64
		wantInflight int64
	}{
		{
			name:         "nothing in flight returns at once",
			inflight:     0,
			act:          func(*Dispatcher, context.CancelFunc) {},
			wantInflight: 0,
		},
		{
			name: "waits until the collector is done", inflight: 1, wantInflight: 0,
			act: func(disp *Dispatcher, _ context.CancelFunc) { disp.inflight.Add(-1) },
		},
		{
			name: "a passed deadline stops the wait", inflight: 1, wantInflight: 1,
			act: func(_ *Dispatcher, cancel context.CancelFunc) { cancel() },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			disp := NewDispatcher(&DispatcherConfig{})
			disp.inflight.Store(tt.inflight)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			done := make(chan struct{})

			go func() {
				disp.waitCollectors(ctx)
				close(done)
			}()

			tt.act(disp, cancel)

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

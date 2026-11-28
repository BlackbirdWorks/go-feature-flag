package exporter_test

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/exporter"
)

// signalExporter tells on a channel each time it exports, and keeps what it exported.
type signalExporter struct {
	exported chan int
	// err is what Export returns.
	err   error
	mu    sync.Mutex
	total int
}

func (*signalExporter) IsBulk() bool { return true }

func (s *signalExporter) Export(_ context.Context, _ *slog.Logger, batch []exporter.ExportableEvent) error {
	s.mu.Lock()
	s.total += len(batch)
	s.mu.Unlock()

	s.exported <- len(batch)

	return s.err
}

const nameNoLimit = "no limit"

func bytesEvent(key string) *exporter.FeatureEvent {
	return &exporter.FeatureEvent{Kind: "feature", Key: key, UserKey: "u"}
}

// TestEventSize: the size of an event is the length of its JSON form with its line break.
func TestEventSize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		key  string
	}{
		{name: "short key", key: "a"},
		{name: "long key", key: strings.Repeat("k", 100)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			event := bytesEvent(tt.key)
			raw, err := event.FormatInJSON()
			require.NoError(t, err)

			assert.Equal(t, len(raw), exporter.EventSize(event))
		})
	}
}

// TestNewManager_MaxBytes: a manager flushes an exporter as soon as the events it holds for it reach MaxBytes,
// whatever their count and the flush interval; without the limit, nothing is exported until the manager stops.
func TestNewManager_MaxBytes(t *testing.T) {
	t.Parallel()

	one := int64(exporter.EventSize(bytesEvent("flag-0")))

	tests := []struct {
		name      string
		maxBytes  int64
		events    int
		wantFlush int
	}{
		{name: "limit reached", maxBytes: 3 * one, events: 3, wantFlush: 3},
		{name: "limit not reached", maxBytes: 4 * one, events: 3, wantFlush: 0},
		{name: nameNoLimit, maxBytes: 0, events: 3, wantFlush: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sink := &signalExporter{exported: make(chan int, 16)}
			mgr := exporter.NewManager[*exporter.FeatureEvent]([]exporter.Config{{
				Exporter: sink, FlushInterval: time.Hour, MaxEventInMemory: 1000, MaxBytes: tt.maxBytes,
			}}, 0, nil)
			mgr.Start()

			for i := range tt.events {
				mgr.AddEvent(bytesEvent("flag-" + string(rune('0'+i))))
			}

			// AddEvent flushes in the caller: what the limit triggers is exported when it returns
			flushed := 0

			select {
			case flushed = <-sink.exported:
			default:
			}

			assert.Equal(t, tt.wantFlush, flushed)

			mgr.Stop()

			sink.mu.Lock()
			defer sink.mu.Unlock()

			assert.Equal(t, tt.events, sink.total, "every event is exported by the end")
		})
	}
}

// sizedStore is the event store with the sizer of the manager set.
func sizedStore(tb testing.TB, limit int64) exporter.EventStore[*exporter.FeatureEvent] {
	tb.Helper()

	store := exporter.NewEventStoreWithLimit[*exporter.FeatureEvent](time.Hour, limit, nil)
	tb.Cleanup(store.Stop)

	sized, ok := store.(interface {
		SetSizer(func(*exporter.FeatureEvent) int)
	})
	require.True(tb, ok)
	sized.SetSizer(func(event *exporter.FeatureEvent) int { return exporter.EventSize(event) })

	store.AddConsumer("c")

	return store
}

type batchProcessor interface {
	ProcessPendingBatches(
		consumerID string, maxBytes int64, process func(context.Context, []*exporter.FeatureEvent) error,
	) error
	PendingBytes(consumerID string) int64
}

// TestEventStore_ProcessPendingBatches: the pending events go out in batches of at most the limit (an event larger
// than it alone), the offset moves after each batch that succeeds, and a batch that fails leaves itself and the
// ones after it pending: what went out is not sent again.
func TestEventStore_ProcessPendingBatches(t *testing.T) {
	t.Parallel()

	one := int64(exporter.EventSize(bytesEvent("flag-0")))

	tests := []struct {
		name        string
		wantBatches []int
		wantAgain   []int
		maxBytes    int64
		failOn      int
		wantErr     bool
	}{
		{name: "one batch", maxBytes: 10 * one, wantBatches: []int{5}},
		{name: "cut by size", maxBytes: 2 * one, wantBatches: []int{2, 2, 1}},
		{name: "event larger than the limit", maxBytes: one - 1, wantBatches: []int{1, 1, 1, 1, 1}},
		{
			name: "second batch fails", maxBytes: 2 * one, failOn: 2, wantErr: true, wantBatches: []int{2},
			wantAgain: []int{2, 1},
		},
		{
			name: "first batch fails", maxBytes: 2 * one, failOn: 1, wantErr: true, wantBatches: []int{},
			wantAgain: []int{2, 2, 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := sizedStore(t, 0)
			for _, key := range []string{"flag-0", "flag-1", "flag-2", "flag-3", "flag-4"} {
				store.Add(bytesEvent(key))
			}

			batches, ok := store.(batchProcessor)
			require.True(t, ok)
			assert.Equal(t, 5*one, batches.PendingBytes("c"))

			run := func(failOn int) ([]int, error) {
				got := []int{}
				call := 0

				err := batches.ProcessPendingBatches(
					"c",
					tt.maxBytes,
					func(_ context.Context, batch []*exporter.FeatureEvent) error {
						call++
						if call == failOn {
							return assert.AnError
						}

						got = append(got, len(batch))

						return nil
					},
				)

				return got, err
			}

			got, err := run(tt.failOn)
			assert.Equal(t, tt.wantBatches, got)
			assert.Equal(t, tt.wantErr, err != nil)

			if tt.wantErr {
				again, againErr := run(0)
				require.NoError(t, againErr)
				assert.Equal(
					t,
					tt.wantAgain,
					again,
					"the batch that failed and the ones after it go out, the others do not",
				)
			}

			assert.Zero(t, batches.PendingBytes("c"))
		})
	}
}

// TestEventStore_PendingBytes: the events a full store dropped do not count in the pending size.
func TestEventStore_PendingBytes(t *testing.T) {
	t.Parallel()

	one := int64(exporter.EventSize(bytesEvent("flag-0")))

	tests := []struct {
		name      string
		limit     int64
		wantBytes int64
	}{
		{name: nameNoLimit, limit: 0, wantBytes: 4 * one},
		{name: "full store", limit: 2, wantBytes: 2 * one},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := sizedStore(t, tt.limit)
			for _, key := range []string{"flag-0", "flag-1", "flag-2", "flag-3"} {
				store.Add(bytesEvent(key))
			}

			batches, ok := store.(batchProcessor)
			require.True(t, ok)
			assert.Equal(t, tt.wantBytes, batches.PendingBytes("c"))
		})
	}
}

// formatCounter is an event that counts how many times it is formatted.
type formatCounter struct {
	exporter.FeatureEvent

	formats *atomic.Int64
}

func (f *formatCounter) FormatInJSON() ([]byte, error) {
	f.formats.Add(1)

	return f.FeatureEvent.FormatInJSON()
}

// TestNewManager_SizesAnEventOnce: with a byte limit an event is formatted once for its size, when the store takes
// it, and the size goes with it to the batches.
func TestNewManager_SizesAnEventOnce(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		maxBytes    int64
		wantFormats int64
	}{
		{name: "limit", maxBytes: 100, wantFormats: 6},
		{name: nameNoLimit, maxBytes: 0, wantFormats: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var formats atomic.Int64

			sink := &signalExporter{exported: make(chan int, 64)}
			mgr := exporter.NewManager[*formatCounter]([]exporter.Config{{
				Exporter: sink, FlushInterval: time.Hour, MaxEventInMemory: 1000, MaxBytes: tt.maxBytes,
			}}, 0, nil)
			mgr.Start()

			for i := range 6 {
				event := &formatCounter{formats: &formats}
				event.Kind, event.Key = "feature", "flag-"+string(rune('0'+i))
				mgr.AddEvent(event)
			}

			mgr.Stop()

			sink.mu.Lock()
			defer sink.mu.Unlock()

			assert.Equal(t, 6, sink.total)
			assert.Equal(t, tt.wantFormats, formats.Load())
		})
	}
}

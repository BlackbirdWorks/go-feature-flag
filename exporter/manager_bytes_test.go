package exporter_test

import (
	"context"
	"log/slog"
	"strings"
	"sync"
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
		{name: "no limit", maxBytes: 0, events: 3, wantFlush: 0},
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

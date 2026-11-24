package exporter_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/exporter"
)

// A handoff larger than what the store can hold must not lose events when an idle exporter can
// take them: reaching the flush threshold in the middle of the handoff lets the exporter make room.
func Test_Manager_QueueEventsLargerThanTheQueueLimit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		limit     int64
		threshold int64
		handoffs  []int
		exporters int
	}{
		{name: "handoff twice the limit", limit: 10, threshold: 10, handoffs: []int{20}, exporters: 1},
		{name: "handoff not a multiple of the limit", limit: 10, threshold: 10, handoffs: []int{25, 3}, exporters: 1},
		{name: "threshold under the limit", limit: 20, threshold: 5, handoffs: []int{100}, exporters: 1},
		{name: "two idle exporters", limit: 10, threshold: 10, handoffs: []int{35}, exporters: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			exps := make([]*countingExporter, tt.exporters)
			cfgs := make([]exporter.Config, tt.exporters)
			for i := range exps {
				exps[i] = &countingExporter{bulk: true}
				cfgs[i] = exporter.Config{
					Exporter: exps[i], FlushInterval: time.Hour,
					MaxEventInMemory: tt.threshold, MaxQueuedEvents: tt.limit,
				}
			}
			m := exporter.NewManager[exporter.FeatureEvent](cfgs, time.Hour, nil)
			total := 0
			for _, n := range tt.handoffs {
				m.(exporter.AsyncManager[exporter.FeatureEvent]).QueueEvents(make([]exporter.FeatureEvent, n))
				total += n
			}
			m.Stop()
			assert.Zero(t, m.(exporter.DroppedEventsCounter).DroppedEvents())
			for _, e := range exps {
				assert.Equal(t, int64(total), e.events.Load())
			}
		})
	}
}

package exporter_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/exporter"
)

// TestEventStore_ProcessPendingBatchesLimitDuringExport: events that arrive while a batch is exported push the consumer
// over its limit. What the consumer gave up is not sent afterwards, and what was in flight is not counted dropped:
// every event is delivered once or counted dropped once.
func TestEventStore_ProcessPendingBatchesLimitDuringExport(t *testing.T) {
	t.Parallel()

	const eventSize = 10

	tests := []struct {
		name          string
		wantDelivered []string
		limit         int64
		maxBytes      int64
		initial       int
		during        int
		wantDropped   int64
		wantPending   int64
	}{
		{
			name: "limit crossed during the first batch", limit: 4, maxBytes: 2 * eventSize, initial: 4, during: 4,
			wantDelivered: []string{"a0", "a1"}, wantDropped: 2, wantPending: 4,
		},
		{
			name: "limit not crossed", limit: 4, maxBytes: 2 * eventSize, initial: 4, during: 0,
			wantDelivered: []string{"a0", "a1", "a2", "a3"}, wantDropped: 0,
		},
		{
			name: "far over the limit", limit: 4, maxBytes: 2 * eventSize, initial: 4, during: 10,
			wantDelivered: []string{"a0", "a1"}, wantDropped: 8, wantPending: 4,
		},
		{
			name: "one big batch is all in flight", limit: 4, maxBytes: 100 * eventSize, initial: 4, during: 4,
			wantDelivered: []string{"a0", "a1", "a2", "a3"}, wantDropped: 0, wantPending: 4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := exporter.NewEventStoreWithLimit[*exporter.FeatureEvent](time.Hour, 0, nil)
			t.Cleanup(store.Stop)

			configurable, ok := store.(interface {
				SetSizer(func(*exporter.FeatureEvent) int)
				SetConsumerLimit(consumerID string, limit int64)
				ProcessPendingBatches(
					consumerID string, maxBytes int64, process func(context.Context, []*exporter.FeatureEvent) error,
				) error
			})
			require.True(t, ok)

			configurable.SetSizer(func(*exporter.FeatureEvent) int { return eventSize })
			store.AddConsumer("c")
			configurable.SetConsumerLimit("c", tt.limit)

			for i := range tt.initial {
				store.Add(bytesEvent(fmt.Sprintf("a%d", i)))
			}

			var delivered []string

			during := false
			err := configurable.ProcessPendingBatches(
				"c", tt.maxBytes, func(_ context.Context, batch []*exporter.FeatureEvent) error {
					for _, event := range batch {
						delivered = append(delivered, event.Key)
					}

					if !during {
						during = true

						for i := range tt.during {
							store.Add(bytesEvent(fmt.Sprintf("d%d", i)))
						}
					}

					return nil
				},
			)
			require.NoError(t, err)

			counter, isCounter := store.(droppedCounter)
			require.True(t, isCounter)

			dropped := counter.DroppedEvents()
			pending, pendingErr := store.GetPendingEventCount("c")
			require.NoError(t, pendingErr)

			assert.Equal(t, tt.wantDelivered, delivered)
			assert.Equal(t, tt.wantDropped, dropped)
			assert.Equal(t, tt.wantPending, pending)
			assert.Equal(
				t, int64(tt.initial+tt.during), int64(len(delivered))+dropped+pending,
				"every event is delivered, dropped or still pending, once",
			)
		})
	}
}

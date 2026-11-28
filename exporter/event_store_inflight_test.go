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

// TestEventStore_LaggardEvictionSparesTheBatchInFlight: a full store does not make the consumer that is furthest behind
// give up an event of the batch it is exporting. The new event is dropped (counted once) instead, and when the batch
// fails its events are still in the store and go out on the next flush.
func TestEventStore_LaggardEvictionSparesTheBatchInFlight(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		wantFirst       []string
		wantRetry       []string
		wantLater       []string
		failFirst       bool
		wantFirstFailed bool
		wantDropped     int64
	}{
		{
			name: "the batch succeeds", wantFirst: []string{"a1", "a2", "a3", "a4"}, wantLater: []string{"a6"},
			wantDropped: 1,
		},
		{
			name: "the batch fails", failFirst: true, wantFirstFailed: true,
			wantFirst: []string{"a1", "a2", "a3", "a4"},
			wantRetry: []string{"a1", "a2", "a3", "a4"}, wantLater: []string{"a6"}, wantDropped: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := exporter.NewEventStoreWithLimit[*exporter.FeatureEvent](time.Hour, 4, nil)
			t.Cleanup(store.Stop)

			batches, ok := store.(interface {
				ProcessPendingBatches(
					consumerID string, maxBytes int64, process func(context.Context, []*exporter.FeatureEvent) error,
				) error
			})
			require.True(t, ok)

			store.AddConsumer("lagging")
			store.AddConsumer("healthy")

			for i := 1; i <= 4; i++ {
				store.Add(bytesEvent(fmt.Sprintf("a%d", i)))
			}

			consumeAll := func(context.Context, []*exporter.FeatureEvent) error { return nil }

			// the healthy consumer is up to date, the lagging one exports its batch while a new event arrives
			require.NoError(t, store.ProcessPendingEvents("healthy", consumeAll))

			run := func(fail bool, during *exporter.FeatureEvent) ([]string, error) {
				var got []string

				err := batches.ProcessPendingBatches(
					"lagging", 1<<20, func(_ context.Context, batch []*exporter.FeatureEvent) error {
						for _, event := range batch {
							got = append(got, event.Key)
						}

						if during != nil {
							store.Add(during)
						}

						if fail {
							return assert.AnError
						}

						return nil
					},
				)

				return got, err
			}

			first, err := run(tt.failFirst, bytesEvent("a5"))
			assert.Equal(t, tt.wantFirstFailed, err != nil)
			assert.Equal(t, tt.wantFirst, first)

			if tt.failFirst {
				retry, retryErr := run(false, nil)
				require.NoError(t, retryErr)
				assert.Equal(t, tt.wantRetry, retry, "the events of the failed batch were not given up")
			}

			require.NoError(t, store.ProcessPendingEvents("healthy", consumeAll))

			// the store is not full for ever: the next event is admitted
			store.Add(bytesEvent("a6"))

			later, laterErr := run(false, nil)
			require.NoError(t, laterErr)
			assert.Equal(t, tt.wantLater, later)

			counter, isCounter := store.(droppedCounter)
			require.True(t, isCounter)
			assert.Equal(t, tt.wantDropped, counter.DroppedEvents(), "the new event was dropped once, nothing else")
		})
	}
}

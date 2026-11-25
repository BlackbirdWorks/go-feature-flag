package exporter_test

import (
	"context"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/testutils"
)

const defaultTestCleanQueueDuration = 100 * time.Millisecond

func Test_ConsumerNameInvalid(t *testing.T) {
	t.Parallel()

	t.Run(
		"GetPendingEventCount: should return an error if the consumer name is invalid",
		func(t *testing.T) {
			t.Parallel()

			eventStore := exporter.NewEventStore[testutils.ExportableMockEvent](
				defaultTestCleanQueueDuration,
			)

			eventStore.AddConsumer("consumer1")
			defer eventStore.Stop()

			count, err := eventStore.GetPendingEventCount("wrong name")
			assert.Equal(t, int64(0), count)
			assert.EqualError(t, err, "consumer with name wrong name not found")
		},
	)
	t.Run(
		"ProcessPendingEvents: should return an error if the consumer name is invalid",
		func(t *testing.T) {
			t.Parallel()

			eventStore := exporter.NewEventStore[testutils.ExportableMockEvent](
				defaultTestCleanQueueDuration,
			)

			eventStore.AddConsumer("consumer1")
			defer eventStore.Stop()

			err := eventStore.ProcessPendingEvents(
				"wrong name",
				func(_ context.Context, _ []testutils.ExportableMockEvent) error { return nil },
			)
			assert.EqualError(t, err, "consumer with name wrong name not found")
		},
	)
}

func Test_SingleConsumer(t *testing.T) {
	t.Parallel()

	consumerName := "consumer1"
	eventStore := exporter.NewEventStore[testutils.ExportableMockEvent](
		defaultTestCleanQueueDuration,
	)

	eventStore.AddConsumer(consumerName)
	defer eventStore.Stop()

	got, _ := eventStore.GetPendingEventCount(consumerName)
	assert.Equal(t, int64(0), got)
	assert.Equal(t, int64(0), eventStore.GetTotalEventCount())

	// start producer
	ctx, cancel := context.WithCancel(context.Background())
	go startEventProducer(ctx, eventStore, 100, false)

	requireEventuallyPendingCount(t, eventStore, consumerName, 100)
	cancel() // stop producing

	// Consume
	err := eventStore.ProcessPendingEvents(consumerName,
		func(_ context.Context, events []testutils.ExportableMockEvent) error {
			assert.Len(t, events, 100)

			return nil
		})
	require.NoError(t, err)

	got, _ = eventStore.GetPendingEventCount(consumerName)
	assert.Equal(t, int64(0), got)

	// restart producing
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()

	go startEventProducer(ctx2, eventStore, 91, false)

	requireEventuallyPendingCount(t, eventStore, consumerName, 91)

	err = eventStore.ProcessPendingEvents(consumerName,
		func(_ context.Context, events []testutils.ExportableMockEvent) error {
			assert.Len(t, events, 91)

			return nil
		})
	require.NoError(t, err)

	requireEventuallyEmptyStore(t, eventStore)
}

func Test_MultipleConsumersSingleThread(t *testing.T) {
	t.Parallel()

	consumerNames := []string{"consumer1", "consumer2"}

	eventStore := exporter.NewEventStore[testutils.ExportableMockEvent](
		defaultTestCleanQueueDuration,
	)
	for _, name := range consumerNames {
		eventStore.AddConsumer(name)
	}

	defer eventStore.Stop()
	// start producer
	ctx, cancelProducer1 := context.WithCancel(context.Background())
	defer cancelProducer1()

	startEventProducer(ctx, eventStore, 1000, false)
	cancelProducer1()
	assert.Equal(t, int64(1000), eventStore.GetTotalEventCount())

	// Consume with Consumer1 only
	consumer1Size, err := eventStore.GetPendingEventCount(consumerNames[0])
	require.NoError(t, err)
	assert.Equal(t, int64(1000), consumer1Size)

	err = eventStore.ProcessPendingEvents(consumerNames[0],
		func(_ context.Context, events []testutils.ExportableMockEvent) error {
			assert.Len(t, events, 1000)

			return nil
		})
	require.NoError(t, err)

	// Produce a second time
	ctx, cancelProducer2 := context.WithCancel(context.Background())
	defer cancelProducer2()

	startEventProducer(ctx, eventStore, 1000, false)
	cancelProducer2()

	// Check queue size
	assert.Equal(t, int64(2000), eventStore.GetTotalEventCount())
	consumer1Size, err = eventStore.GetPendingEventCount(consumerNames[0])
	require.NoError(t, err)
	assert.Equal(t, int64(1000), consumer1Size)

	consumer2Size, err := eventStore.GetPendingEventCount(consumerNames[1])
	require.NoError(t, err)
	assert.Equal(t, int64(2000), consumer2Size)

	// Consumer with Consumer1 and Consumer2
	err = eventStore.ProcessPendingEvents(consumerNames[0],
		func(_ context.Context, events []testutils.ExportableMockEvent) error {
			assert.Len(t, events, 1000)

			return nil
		})
	require.NoError(t, err)

	err = eventStore.ProcessPendingEvents(consumerNames[1],
		func(_ context.Context, events []testutils.ExportableMockEvent) error {
			assert.Len(t, events, 2000)

			return nil
		})
	require.NoError(t, err)

	// Check garbage collector
	requireEventuallyEmptyStore(t, eventStore)
}

func Test_MultipleConsumersMultipleGORoutines(t *testing.T) {
	t.Parallel()

	consumerNames := []string{"consumer1", "consumer2"}

	eventStore := exporter.NewEventStore[testutils.ExportableMockEvent](
		defaultTestCleanQueueDuration,
	)
	for _, name := range consumerNames {
		eventStore.AddConsumer(name)
	}

	defer eventStore.Stop()
	// start producer
	ctx, cancelProducer1 := context.WithCancel(context.Background())
	defer cancelProducer1()

	go startEventProducer(ctx, eventStore, 100000, true)

	time.Sleep(50 * time.Millisecond)

	wg := &sync.WaitGroup{}

	var countersMu sync.Mutex

	consumeFunc := func(
		eventStore exporter.EventStore[testutils.ExportableMockEvent],
		consumerName string,
		eventCounters *map[string]int,
	) {
		defer wg.Done()

		err := eventStore.ProcessPendingEvents(consumerName,
			func(_ context.Context, events []testutils.ExportableMockEvent) error {
				assert.NotEmpty(t, events)

				return nil
			})
		require.NoError(t, err)
		time.Sleep(
			50 * time.Millisecond,
		) // we wait to be sure that the producer has produce new events

		err = eventStore.ProcessPendingEvents(consumerName,
			func(_ context.Context, events []testutils.ExportableMockEvent) error {
				if eventCounters != nil {
					countersMu.Lock()
					(*eventCounters)[consumerName] = len(events)
					countersMu.Unlock()
				}

				return nil
			})
		assert.NoError(t, err)
	}

	wg.Add(2)

	eventCounters := map[string]int{}
	go consumeFunc(eventStore, consumerNames[0], &eventCounters)
	go consumeFunc(eventStore, consumerNames[1], &eventCounters)

	wg.Wait()

	assert.Positive(t, eventCounters[consumerNames[0]])
	assert.Positive(t, eventCounters[consumerNames[1]])
}

func Test_ProcessPendingEventInError(t *testing.T) {
	t.Parallel()

	consumerName := "consumer1"
	eventStore := exporter.NewEventStore[testutils.ExportableMockEvent](
		defaultTestCleanQueueDuration,
	)

	eventStore.AddConsumer(consumerName)
	defer eventStore.Stop()
	// start producer
	startEventProducer(context.TODO(), eventStore, 1000, false)
	assert.Equal(t, int64(1000), eventStore.GetTotalEventCount())

	consumer1Size, err := eventStore.GetPendingEventCount(consumerName)
	assert.Equal(t, 1000, int(consumer1Size))
	require.NoError(t, err)

	// process is in error, so we are not able to update the offset
	err = eventStore.ProcessPendingEvents(
		consumerName,
		func(_ context.Context, events []testutils.ExportableMockEvent) error {
			assert.Len(t, events, 1000)

			return errError
		},
	)
	require.Error(t, err)

	// We still have the same number of items waiting for next process
	consumer1Size, err = eventStore.GetPendingEventCount(consumerName)
	assert.Equal(t, 1000, int(consumer1Size))
	require.NoError(t, err)

	// process is not in error anymore
	err = eventStore.ProcessPendingEvents(
		consumerName,
		func(_ context.Context, events []testutils.ExportableMockEvent) error {
			assert.Len(t, events, 1000)

			return nil
		},
	)
	require.NoError(t, err)

	// we have consumed all the items
	consumer1Size, err = eventStore.GetPendingEventCount(consumerName)
	assert.Equal(t, 0, int(consumer1Size))
	assert.NoError(t, err)
}

func Test_WaitForEmptyClean(t *testing.T) {
	t.Parallel()

	consumerNames := []string{"consumer1"}

	eventStore := exporter.NewEventStore[testutils.ExportableMockEvent](
		defaultTestCleanQueueDuration,
	)
	for _, name := range consumerNames {
		eventStore.AddConsumer(name)
	}

	defer eventStore.Stop()

	// start producer
	ctx := context.Background()
	startEventProducer(ctx, eventStore, 100, false)
	err := eventStore.ProcessPendingEvents(
		consumerNames[0],
		func(_ context.Context, events []testutils.ExportableMockEvent) error {
			assert.Len(t, events, 100)

			return nil
		},
	)
	require.NoError(t, err)
	assert.Positive(t, eventStore.GetTotalEventCount())
	requireEventuallyEmptyStore(t, eventStore)
}

func Test_ProcessPendingEvents_DoesNotBlockAdd(t *testing.T) {
	t.Parallel()

	consumerName := "consumer1"
	eventStore := exporter.NewEventStore[testutils.ExportableMockEvent](
		defaultTestCleanQueueDuration,
	)

	eventStore.AddConsumer(consumerName)
	defer eventStore.Stop()

	for range 10 {
		eventStore.Add(testutils.NewExportableMockEvent("init"))
	}

	processingStarted := make(chan struct{})
	release := make(chan struct{})
	processingDone := make(chan struct{})

	var processedBatch []string

	go func() {
		_ = eventStore.ProcessPendingEvents(consumerName,
			func(_ context.Context, events []testutils.ExportableMockEvent) error {
				for _, e := range events {
					processedBatch = append(processedBatch, e.GetUserKey())
				}

				close(processingStarted)
				<-release

				return nil
			})

		close(processingDone)
	}()

	<-processingStarted

	// the exporter is still running: Add must not wait for it
	addDone := make(chan struct{})

	go func() {
		eventStore.Add(testutils.NewExportableMockEvent("during-export"))
		close(addDone)
	}()

	select {
	case <-addDone:
	case <-time.After(10 * time.Second): // only bounds a failure, the exporter is blocked until release
		require.Fail(t, "Add() was blocked while ProcessPendingEvents was running a slow exporter")
	}

	close(release)
	<-processingDone

	// the exporter got the 10 events that were pending when it started, the one added meanwhile still is
	assert.Equal(t, []string{"init", "init", "init", "init", "init", "init", "init", "init", "init", "init"},
		processedBatch)

	pending, err := eventStore.GetPendingEventCount(consumerName)
	require.NoError(t, err)
	assert.Equal(t, int64(1), pending)
}

func Test_ProcessPendingEvents_QueuedSameConsumerCallDoesNotBlockAdd(t *testing.T) {
	t.Parallel()

	consumerName := "consumer1"
	eventStore := exporter.NewEventStore[testutils.ExportableMockEvent](
		defaultTestCleanQueueDuration,
	)

	eventStore.AddConsumer(consumerName)
	defer eventStore.Stop()

	for range 10 {
		eventStore.Add(testutils.NewExportableMockEvent("init"))
	}

	processingStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondCallStarted := make(chan struct{})

	var (
		wg         sync.WaitGroup
		batchesMu  sync.Mutex
		batchSizes []int
	)

	wg.Add(2)
	go func() {
		defer wg.Done()

		assert.NoError(t, eventStore.ProcessPendingEvents(consumerName,
			func(_ context.Context, events []testutils.ExportableMockEvent) error {
				batchesMu.Lock()

				batchSizes = append(batchSizes, len(events))
				batchesMu.Unlock()

				select {
				case <-processingStarted:
				default:
					close(processingStarted)
				}

				<-releaseFirst

				return nil
			},
		))
	}()

	<-processingStarted

	go func() {
		close(secondCallStarted)
		assert.NoError(t, eventStore.ProcessPendingEvents(consumerName,
			func(_ context.Context, events []testutils.ExportableMockEvent) error {
				batchesMu.Lock()

				batchSizes = append(batchSizes, len(events))
				batchesMu.Unlock()

				select {
				case <-processingStarted:
				default:
					close(processingStarted)
				}

				<-releaseFirst

				return nil
			},
		))
		wg.Done()
	}()

	<-secondCallStarted
	time.Sleep(20 * time.Millisecond)

	addDone := make(chan struct{})

	go func() {
		eventStore.Add(testutils.NewExportableMockEvent("during-queued-flush"))
		close(addDone)
	}()

	select {
	case <-addDone:
	case <-time.After(10 * time.Second): // only bounds a failure, the first call is blocked until releaseFirst
		require.Fail(t, "Add() was blocked while a same-consumer ProcessPendingEvents call was queued")
	}

	close(releaseFirst)
	wg.Wait()

	// the first call took the 10 events that were pending, the queued one runs after it and takes the one
	// added while it waited
	batchesMu.Lock()
	defer batchesMu.Unlock()

	assert.Equal(t, []int{10, 1}, batchSizes)

	pending, err := eventStore.GetPendingEventCount(consumerName)
	require.NoError(t, err)
	assert.Equal(t, int64(0), pending)
}

func Test_ProcessPendingEvents_SameConsumerConcurrentCallsDoNotDuplicate(t *testing.T) {
	t.Parallel()

	consumerName := "consumer1"
	eventStore := exporter.NewEventStore[testutils.ExportableMockEvent](
		defaultTestCleanQueueDuration,
	)

	eventStore.AddConsumer(consumerName)
	defer eventStore.Stop()

	for range 10 {
		eventStore.Add(testutils.NewExportableMockEvent("init"))
	}

	var (
		callbackMu  sync.Mutex
		batchSizes  []int
		startedOnce sync.Once
		started     = make(chan struct{})
		release     = make(chan struct{})
		wg          sync.WaitGroup
	)

	processFunc := func(ctx context.Context, events []testutils.ExportableMockEvent) error {
		callbackMu.Lock()

		batchSizes = append(batchSizes, len(events))
		callbackMu.Unlock()
		startedOnce.Do(func() { close(started) })
		<-release

		err := ctx.Err()
		if err != nil {
			return err
		}

		return nil
	}

	wg.Add(2)
	go func() {
		defer wg.Done()

		assert.NoError(t, eventStore.ProcessPendingEvents(consumerName, processFunc))
	}()

	<-started

	go func() {
		defer wg.Done()

		assert.NoError(t, eventStore.ProcessPendingEvents(consumerName, processFunc))
	}()

	close(release)
	wg.Wait()

	callbackMu.Lock()
	defer callbackMu.Unlock()

	assert.Equal(t, []int{10}, batchSizes)

	count, err := eventStore.GetPendingEventCount(consumerName)
	require.NoError(t, err)
	assert.Equal(t, int64(0), count)
}

func Test_ProcessPendingEvents_EmptyBatchSkipsCallback(t *testing.T) {
	t.Parallel()

	consumerName := "consumer1"
	eventStore := exporter.NewEventStore[testutils.ExportableMockEvent](
		defaultTestCleanQueueDuration,
	)

	eventStore.AddConsumer(consumerName)
	defer eventStore.Stop()

	callbackCalled := false
	err := eventStore.ProcessPendingEvents(
		consumerName,
		func(_ context.Context, _ []testutils.ExportableMockEvent) error {
			callbackCalled = true

			return nil
		},
	)

	require.NoError(t, err)
	assert.False(t, callbackCalled)
}

// requireEventuallyPendingCount waits until the producer goroutine has pushed the expected
// number of events. Sleeping for a fixed duration instead makes the test depend on the producer
// being scheduled promptly, which is not guaranteed on a loaded CI runner.
func requireEventuallyPendingCount(
	t *testing.T,
	eventStore exporter.EventStore[testutils.ExportableMockEvent],
	consumerName string,
	want int64,
) {
	t.Helper()
	require.Eventually(t, func() bool {
		got, err := eventStore.GetPendingEventCount(consumerName)

		return err == nil && got == want
	}, 10*time.Second, 10*time.Millisecond,
		"consumer %q never reached %d pending events", consumerName, want)
}

// requireEventuallyEmptyStore waits for the periodic clean queue goroutine to drop the events
// consumed by everybody. It runs on a ticker, so a fixed sleep of one or two intervals has no
// margin left once the runner stalls.
func requireEventuallyEmptyStore(
	t *testing.T,
	eventStore exporter.EventStore[testutils.ExportableMockEvent],
) {
	t.Helper()
	require.Eventually(t, func() bool {
		return eventStore.GetTotalEventCount() == int64(0)
	}, 10*time.Second, 10*time.Millisecond,
		"the periodic clean queue never removed the consumed events")
}

func startEventProducer(
	ctx context.Context,
	eventStore exporter.EventStore[testutils.ExportableMockEvent],
	produceMax int,
	randomizeProducingTime bool,
) {
	for range produceMax {
		select {
		case <-ctx.Done():
			return
		default:
			if randomizeProducingTime {
				randomNumber := rand.Intn(10) + 1
				time.Sleep(time.Duration(randomNumber) * time.Millisecond)
			}

			eventStore.Add(testutils.NewExportableMockEvent("Hello"))
		}
	}
}

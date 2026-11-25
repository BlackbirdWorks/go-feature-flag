package exporter

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"sync"
	"time"

	"github.com/blackbirdworks/semaphore/internal"
)

var (
	errTryingToSendUnknown       = errors.New("trying to send unknown object to the exporter (deprecated)")
	errTryingToSendUnknownObject = errors.New("trying to send unknown object to the exporter")
	errThisIsNotA                = errors.New("this is not a valid exporter")
)

const (
	defaultFlushInterval    = 60 * time.Second
	defaultMaxEventInMemory = int64(100000)
)

// DataExporter is an exporter that receives the evaluation events of type T.
type DataExporter[T ExportableEvent] interface {
	// Start is launching the ticker to periodically flush the data
	Start()
	// Stop is stopping the ticker
	Stop()
	// Flush is sending the data to the exporter
	Flush()
	// IsBulk return false if we should directly send the data as soon as it is produce
	IsBulk() bool
	// GetConsumerID return the consumer ID used in the event store
	GetConsumerID() string
	// GetMaxEventInMemory return the maximum number of event you keep in the cache before calling Flush()
	GetMaxEventInMemory() int64
}

// Config is the configuration of an exporter.
type Config struct {
	Exporter         CommonExporter
	FlushInterval    time.Duration
	MaxEventInMemory int64
	// MaxQueuedEvents is the hard limit of events kept in memory while they wait to be exported.
	// When it is reached (exporter slow or failing) new events are dropped and counted.
	// Default: max(DefaultQueuedEventsFactor x MaxEventInMemory, DefaultMinQueuedEvents).
	MaxQueuedEvents int64
}

type dataExporterImpl[T ExportableEvent] struct {
	eventStore EventStore[T]
	logger     *slog.Logger
	daemonChan chan struct{}
	ticker     *time.Ticker
	consumerID string
	exporter   Config
	// stopOnce guarantees that we stop the daemon only once: Close() can be called several times
	// on a Semaphore client, and closing an already closed channel panics.
	stopOnce sync.Once
}

// NewDataExporter create a new DataExporter with the given exporter and his consumer information to consume the data
// from the shared event store.
func NewDataExporter[T ExportableEvent](exporter Config, consumerID string,
	eventStore EventStore[T], logger *slog.Logger) DataExporter[T] {
	if exporter.FlushInterval == 0 {
		exporter.FlushInterval = defaultFlushInterval
	}

	if exporter.MaxEventInMemory == 0 {
		exporter.MaxEventInMemory = defaultMaxEventInMemory
	}

	return &dataExporterImpl[T]{
		consumerID: consumerID,
		eventStore: eventStore,
		logger:     internal.LoggerOrDiscard(logger),
		exporter:   exporter,
		daemonChan: make(chan struct{}),
		ticker:     time.NewTicker(exporter.FlushInterval),
	}
}

// Start is launching the ticker to periodically flush the data
// If we have a live exporter we don't start the daemon.
func (d *dataExporterImpl[T]) Start() {
	// we don't start the daemon if we are not in bulk mode
	if !d.IsBulk() {
		return
	}

	for {
		select {
		case <-d.ticker.C:
			d.Flush()
		case <-d.daemonChan:
			// stop the daemon
			return
		}
	}
}

// Stop is flushing the data and stopping the ticker
// It is safe to call it several times.
func (d *dataExporterImpl[T]) Stop() {
	// we don't start the daemon if we are not in bulk mode
	if !d.IsBulk() {
		d.Flush()

		return
	}

	d.stopOnce.Do(func() {
		d.ticker.Stop()
		close(d.daemonChan)
	})
	d.Flush()
}

// Flush is sending the data to the exporter.
func (d *dataExporterImpl[T]) Flush() {
	_ = d.flushQueue()
}

// flush is Flush, it also returns the error of the flush (the manager uses it to back off).
func (d *dataExporterImpl[T]) flushQueue() error {
	store := d.eventStore

	err := store.ProcessPendingEvents(d.consumerID, d.sendEvents)
	if err != nil {
		if d != nil && d.logger != nil {
			d.logger.Error("error while exporting the pending events", slog.String("error", err.Error()))
		}

		return fmt.Errorf("process pending events: %w", err)
	}
	// release the events everybody has consumed instead of waiting for the periodic cleaning
	if c, ok := store.(interface{ CleanQueue() }); ok {
		c.CleanQueue()
	}

	return nil
}

// IsBulk return false if we should directly send the data as soon as it is produce.
func (d *dataExporterImpl[T]) IsBulk() bool {
	return d.exporter.Exporter.IsBulk()
}

// GetConsumerID return the consumer ID used in the event store.
func (d *dataExporterImpl[T]) GetConsumerID() string {
	return d.consumerID
}

// GetMaxEventInMemory return the maximum number of event you keep in the cache before calling Flush().
func (d *dataExporterImpl[T]) GetMaxEventInMemory() int64 {
	return d.exporter.MaxEventInMemory
}

// sendEvents is sending the events to the exporter.
func (d *dataExporterImpl[T]) sendEvents(ctx context.Context, events []T) error {
	if len(events) == 0 {
		return nil
	}

	switch exp := d.exporter.Exporter.(type) {
	case DeprecatedExporterV1:
		return d.sendEventsV1(ctx, exp, events)
	case DeprecatedExporterV2:
		return d.sendEventsV2(ctx, exp, events)
	case Exporter:
		exportableEvents := make([]ExportableEvent, len(events))
		for i, event := range events {
			exportableEvents[i] = ExportableEvent(event)
		}

		err := exp.Export(ctx, d.logger, exportableEvents)
		if err != nil {
			return fmt.Errorf("error while exporting data: %w", err)
		}

		return nil
	default:
		return errThisIsNotA
	}
}

// sendEventsV1 sends the events to an exporter using the legacy logger.
func (d *dataExporterImpl[T]) sendEventsV1(ctx context.Context, exp DeprecatedExporterV1, events []T) error {
	var legacyLogger *log.Logger
	if d.logger != nil {
		legacyLogger = slog.NewLogLogger(d.logger.Handler(), slog.LevelError)
	}

	featureEvents, ok := any(events).([]FeatureEvent)
	if !ok {
		return errTryingToSendUnknown
	}

	// use dc exporter as a DeprecatedExporterV1
	err := exp.Export(ctx, legacyLogger, featureEvents)
	d.logger.WarnContext(ctx, "You are using an exporter with the old logger."+
		"Please update your custom exporter to comply to the new Exporter interface.")

	if err != nil {
		return fmt.Errorf("error while exporting data (deprecated): %w", err)
	}

	return nil
}

// sendEventsV2 sends the events to an exporter of the deprecated v2 interface.
func (d *dataExporterImpl[T]) sendEventsV2(ctx context.Context, exp DeprecatedExporterV2, events []T) error {
	featureEvents, ok := any(events).([]FeatureEvent)
	if !ok {
		return errTryingToSendUnknownObject
	}

	err := exp.Export(ctx, d.logger, featureEvents)
	if err != nil {
		return fmt.Errorf("error while exporting data: %w", err)
	}

	return nil
}

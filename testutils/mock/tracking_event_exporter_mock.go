package mock

import (
	"context"
	"sync"

	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/utils/fflog"
)

type TrackingEventExporter struct {
	Err               error
	ExportedEvents    []exporter.TrackingEvent
	ExpectedNumberErr int
	CurrentNumberErr  int
	once              sync.Once
	mutex             sync.Mutex
	Bulk              bool
}

func (m *TrackingEventExporter) Export(
	_ context.Context,
	_ *fflog.FFLogger,
	events []exporter.ExportableEvent,
) error {
	m.once.Do(m.initMutex)
	m.mutex.Lock()
	defer m.mutex.Unlock()
	switch events := any(events).(type) {
	case []exporter.ExportableEvent:
		t := make([]exporter.TrackingEvent, len(events))
		for i, v := range events {
			t[i] = v.(exporter.TrackingEvent) //nolint:errcheck // test mock: the events are TrackingEvent by contract
		}
		m.ExportedEvents = append(m.ExportedEvents, t...)
	case []exporter.TrackingEvent:
		m.ExportedEvents = append(m.ExportedEvents, events...)
	}
	if m.Err != nil {
		if m.ExpectedNumberErr > m.CurrentNumberErr {
			m.CurrentNumberErr++

			return m.Err
		}
	}

	return nil
}

func (m *TrackingEventExporter) GetExportedEvents() []exporter.ExportableEvent {
	m.once.Do(m.initMutex)
	m.mutex.Lock()
	defer m.mutex.Unlock()
	trackingEvents := make([]exporter.ExportableEvent, 0, len(m.ExportedEvents))
	for _, event := range m.ExportedEvents {
		trackingEvents = append(trackingEvents, event)
	}

	return trackingEvents
}

func (m *TrackingEventExporter) IsBulk() bool {
	return m.Bulk
}

func (m *TrackingEventExporter) initMutex() {
	m.mutex = sync.Mutex{}
}

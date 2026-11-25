package mock

import (
	"context"
	"log"
	"sync"

	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/utils/fflog"
)

// ExporterMock is the interface implemented by the exporter mocks.
type ExporterMock interface {
	exporter.CommonExporter
	GetExportedEvents() []exporter.ExportableEvent
}

// Exporter is a mock exporter that records the events it exports.
type Exporter struct {
	Err               error
	ExportedEvents    []exporter.ExportableEvent
	ExpectedNumberErr int
	CurrentNumberErr  int
	once              sync.Once
	mutex             sync.Mutex
	Bulk              bool
}

// Export records the events and returns the configured error.
func (m *Exporter) Export(
	_ context.Context,
	_ *fflog.FFLogger,
	events []exporter.ExportableEvent,
) error {
	m.once.Do(m.initMutex)
	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.ExportedEvents = append(m.ExportedEvents, events...)
	if m.Err != nil {
		if m.ExpectedNumberErr > m.CurrentNumberErr {
			m.CurrentNumberErr++

			return m.Err
		}
	}

	return nil
}

// GetExportedEvents returns the events recorded by the mock exporter.
func (m *Exporter) GetExportedEvents() []exporter.ExportableEvent {
	m.once.Do(m.initMutex)
	m.mutex.Lock()
	defer m.mutex.Unlock()

	return m.ExportedEvents
}

// IsBulk reports whether the mock exporter sends events in bulk.
func (m *Exporter) IsBulk() bool {
	return m.Bulk
}

func (m *Exporter) initMutex() {
	m.mutex = sync.Mutex{}
}

// ExporterDeprecated -----.
type ExporterDeprecated struct {
	Err               error
	ExportedEvents    []exporter.FeatureEvent
	ExpectedNumberErr int
	CurrentNumberErr  int
	once              sync.Once
	mutex             sync.Mutex
	Bulk              bool
}

// Export records the events and returns the configured error.
func (m *ExporterDeprecated) Export(
	_ context.Context,
	_ *log.Logger,
	events []exporter.FeatureEvent,
) error {
	m.once.Do(m.initMutex)
	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.ExportedEvents = append(m.ExportedEvents, events...)
	if m.Err != nil {
		if m.ExpectedNumberErr > m.CurrentNumberErr {
			m.CurrentNumberErr++

			return m.Err
		}
	}

	return nil
}

// GetExportedEvents returns the events recorded by the deprecated mock exporter.
func (m *ExporterDeprecated) GetExportedEvents() []exporter.ExportableEvent {
	m.once.Do(m.initMutex)
	m.mutex.Lock()
	defer m.mutex.Unlock()

	exportableEvents := make([]exporter.ExportableEvent, len(m.ExportedEvents))
	for index := range m.ExportedEvents {
		exportableEvents[index] = m.ExportedEvents[index]
	}

	return exportableEvents
}

// IsBulk reports whether the deprecated mock exporter sends events in bulk.
func (m *ExporterDeprecated) IsBulk() bool {
	return m.Bulk
}

func (m *ExporterDeprecated) initMutex() {
	m.mutex = sync.Mutex{}
}

// ExporterDeprecatedV2 -----.
type ExporterDeprecatedV2 struct {
	Err               error
	ExportedEvents    []exporter.FeatureEvent
	ExpectedNumberErr int
	CurrentNumberErr  int
	once              sync.Once
	mutex             sync.Mutex
	Bulk              bool
}

// Export records the events and returns the configured error.
func (m *ExporterDeprecatedV2) Export(
	_ context.Context,
	_ *fflog.FFLogger,
	events []exporter.FeatureEvent,
) error {
	m.once.Do(m.initMutex)
	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.ExportedEvents = append(m.ExportedEvents, events...)
	if m.Err != nil {
		if m.ExpectedNumberErr > m.CurrentNumberErr {
			m.CurrentNumberErr++

			return m.Err
		}
	}

	return nil
}

// GetExportedEvents returns the events recorded by the deprecated V2 mock exporter.
func (m *ExporterDeprecatedV2) GetExportedEvents() []exporter.ExportableEvent {
	m.once.Do(m.initMutex)
	m.mutex.Lock()
	defer m.mutex.Unlock()

	exportableEvents := make([]exporter.ExportableEvent, len(m.ExportedEvents))
	for index := range m.ExportedEvents {
		exportableEvents[index] = m.ExportedEvents[index]
	}

	return exportableEvents
}

// IsBulk reports whether the deprecated V2 mock exporter sends events in bulk.
func (m *ExporterDeprecatedV2) IsBulk() bool {
	return m.Bulk
}

func (m *ExporterDeprecatedV2) initMutex() {
	m.mutex = sync.Mutex{}
}

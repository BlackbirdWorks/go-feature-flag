package exporter_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/testhelpers"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
	"github.com/blackbirdworks/semaphore/testhelpers/mock"
	"github.com/blackbirdworks/semaphore/testhelpers/slogutil"
	"github.com/blackbirdworks/semaphore/utils/fflog"
)

const (
	deprecatedExporter = "deprecated exporter"
	slowConsumer       = "slow-consumer"
)

var (
	errError = errors.New("error")
)

type slowExporter struct {
	delay      time.Duration
	exportCall atomic.Int32
}

func (s *slowExporter) Export(ctx context.Context, _ *fflog.FFLogger, _ []exporter.ExportableEvent) error {
	s.exportCall.Add(1)
	time.Sleep(s.delay)

	return ctx.Err()
}

func (*slowExporter) IsBulk() bool {
	return true
}

func TestDataExporterFlush_TriggerError(t *testing.T) {
	t.Parallel()

	evStore := mock.NewEventStore[exporter.FeatureEvent]()
	for range 100 {
		evStore.Add(exporter.FeatureEvent{
			Kind: fixtures.KindFeature,
		})
	}

	logFile, _ := os.CreateTemp(t.TempDir(), "")
	textHandler := slogutil.MessageOnlyHandler{Writer: logFile}
	logger := &fflog.FFLogger{LeveledLogger: slog.New(&textHandler)}

	defer func() { _ = os.Remove(logFile.Name()) }()

	exporterMock := mock.Exporter{}
	exp := exporter.NewDataExporter[exporter.FeatureEvent](exporter.Config{
		Exporter:         &exporterMock,
		FlushInterval:    0,
		MaxEventInMemory: 0,
	}, "error", evStore, logger)

	exp.Flush()
	// flush should error and not return any event
	assert.Empty(t, exporterMock.GetExportedEvents())

	logContent, _ := os.ReadFile(logFile.Name())
	assert.Equal(t, "error\n", string(logContent))
}

func TestDataExporterFlush_TriggerErrorIfNotKnowType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		exporter    mock.ExporterMock
		expectedLog string
	}{
		{
			name:        deprecatedExporter,
			exporter:    &mock.ExporterDeprecated{},
			expectedLog: "trying to send unknown object to the exporter (deprecated)\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			evStore := mock.NewEventStore[testhelpers.ExportableMockEvent]()
			for range 100 {
				evStore.Add(testhelpers.NewExportableMockEvent(fixtures.KindFeature))
			}

			logFile, _ := os.CreateTemp(t.TempDir(), "")
			textHandler := slogutil.MessageOnlyHandler{Writer: logFile}
			logger := &fflog.FFLogger{LeveledLogger: slog.New(&textHandler)}

			defer func() { _ = os.Remove(logFile.Name()) }()

			exporterMock := tt.exporter
			exp := exporter.NewDataExporter[testhelpers.ExportableMockEvent](
				exporter.Config{
					Exporter:         exporterMock,
					FlushInterval:    0,
					MaxEventInMemory: 0,
				},
				"id-consumer",
				evStore,
				logger,
			)

			exp.Flush()
			// flush should error and not return any event
			assert.Empty(t, exporterMock.GetExportedEvents())

			logContent, _ := os.ReadFile(logFile.Name())
			assert.Equal(t, tt.expectedLog, string(logContent))
		})
	}
}

func TestDataExporterFlush_TriggerErrorIfExporterFail(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		exporter    mock.ExporterMock
		expectedLog string
	}{
		{
			name:        "classic exporter",
			exporter:    &mock.Exporter{Err: errError, ExpectedNumberErr: 1},
			expectedLog: "error while exporting data: error\n",
		},
		{
			name:     deprecatedExporter,
			exporter: &mock.ExporterDeprecated{Err: errError, ExpectedNumberErr: 1},
			expectedLog: "You are using an exporter with the old logger.Please update your custom exporter to comply to th" +
				"e new Exporter interface.\nerror while exporting data (deprecated): error\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			evStore := mock.NewEventStore[exporter.FeatureEvent]()
			for range 100 {
				evStore.Add(exporter.FeatureEvent{Kind: fixtures.KindFeature})
			}

			logFile, _ := os.CreateTemp(t.TempDir(), "")
			textHandler := slogutil.MessageOnlyHandler{Writer: logFile}
			logger := &fflog.FFLogger{LeveledLogger: slog.New(&textHandler)}

			defer func() { _ = os.Remove(logFile.Name()) }()

			exporterMock := tt.exporter
			exp := exporter.NewDataExporter[exporter.FeatureEvent](
				exporter.Config{
					Exporter:         exporterMock,
					FlushInterval:    0,
					MaxEventInMemory: 0,
				}, "id-consumer", evStore, logger)

			exp.Flush()
			// flush should error and not return any event
			assert.Len(t, exporterMock.GetExportedEvents(), 100)

			logContent, _ := os.ReadFile(logFile.Name())
			assert.Equal(t, tt.expectedLog, string(logContent))
		})
	}
}

func TestDataExporterFlush_ConcurrentFlushNoDuplicate(t *testing.T) {
	t.Parallel()

	evStore := exporter.NewEventStore[exporter.FeatureEvent](defaultTestCleanQueueDuration)

	evStore.AddConsumer(slowConsumer)
	defer evStore.Stop()

	for range 100 {
		evStore.Add(exporter.FeatureEvent{Kind: fixtures.KindFeature})
	}

	slow := &slowExporter{delay: 200 * time.Millisecond}
	exp := exporter.NewDataExporter[exporter.FeatureEvent](exporter.Config{
		Exporter:         slow,
		FlushInterval:    10 * time.Second,
		MaxEventInMemory: 100000,
	}, slowConsumer, evStore, nil)

	var wg sync.WaitGroup

	wg.Go(func() {
		exp.Flush()
	})

	time.Sleep(10 * time.Millisecond)

	wg.Go(func() {
		exp.Flush()
	})

	wg.Wait()

	assert.Equal(t, int32(1), slow.exportCall.Load())

	count, err := evStore.GetPendingEventCount(slowConsumer)
	require.NoError(t, err)
	assert.Equal(t, int64(0), count)
}

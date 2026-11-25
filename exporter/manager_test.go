package exporter_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"runtime/pprof"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thejerf/slogassert"

	ffclient "github.com/blackbirdworks/semaphore"
	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/retriever/fileretriever"
	"github.com/blackbirdworks/semaphore/testutils/mock"
	"github.com/blackbirdworks/semaphore/utils/fflog"
)

const (
	anonymous   = "anonymous"
	defaultVar2 = "defaultVar"
	leakcheck   = "leakcheck"
	sERVER      = "SERVER"
	testFlag    = "test-flag"
)

var (
	errRandom = errors.New("random err")
)

func TestDataExporterManager_flushWithTime(t *testing.T) {
	t.Parallel()

	tests := []struct {
		mockExporter mock.ExporterMock
		name         string
		wantExported int
	}{
		{
			name:         "flushTime: classic exporter",
			mockExporter: &mock.Exporter{Bulk: true},
			wantExported: 1,
		},
		{
			name:         "flushTime: deprecated exporter",
			mockExporter: &mock.ExporterDeprecated{Bulk: true},
			wantExported: 1,
		},
		{
			name:         "flushTime: deprecated exporter v2",
			mockExporter: &mock.ExporterDeprecatedV2{Bulk: true},
			wantExported: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dataExporterMock := []exporter.Config{
				{
					FlushInterval:    10 * time.Millisecond,
					MaxEventInMemory: 1000,
					Exporter:         tt.mockExporter,
				},
			}

			mgr := exporter.NewManager[exporter.FeatureEvent](
				dataExporterMock,
				exporter.DefaultExporterCleanQueueInterval,
				nil,
			)

			go mgr.Start()
			defer mgr.Stop()

			// Initialize inputEvents slice
			inputEvents := []exporter.FeatureEvent{
				exporter.NewFeatureEvent(
					ffcontext.NewEvaluationContextBuilder(aBCD).
						AddCustom(anonymous, true).
						Build(),
					randomKey,
					"YO",
					defaultVar2,
					false,
					"",
					sERVER,
					nil,
				),
			}

			want := make([]exporter.ExportableEvent, len(inputEvents))
			for i, event := range inputEvents {
				mgr.AddEvent(event)
				want[i] = event
			}

			// the flush by time exports everything: wait for it (the timeout only bounds a failure)
			require.Eventually(t, func() bool {
				return len(tt.mockExporter.GetExportedEvents()) >= len(want)
			}, 30*time.Second, 5*time.Millisecond, "the events were never exported")
			assert.Len(t, tt.mockExporter.GetExportedEvents(), tt.wantExported)
			assert.Equal(t, want, tt.mockExporter.GetExportedEvents())
		})
	}
}

func TestDataExporterManager_flushWithNumberOfEvents(t *testing.T) {
	t.Parallel()

	tests := []struct {
		mockExporter mock.ExporterMock
		name         string
		wantExported int
	}{
		{
			name:         "flushWithNumberOfEvents: classic exporter",
			mockExporter: &mock.Exporter{Bulk: true},
			wantExported: 100,
		},
		{
			name:         "flushWithNumberOfEvents: deprecated exporter",
			mockExporter: &mock.ExporterDeprecated{Bulk: true},
			wantExported: 100,
		},
		{
			name:         "flushWithNumberOfEvents: deprecated exporter v2",
			mockExporter: &mock.ExporterDeprecatedV2{Bulk: true},
			wantExported: 100,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dataExporterMock := []exporter.Config{
				{
					FlushInterval:    time.Hour, // only the number of events triggers the flush, never the ticker
					MaxEventInMemory: 100,
					Exporter:         tt.mockExporter,
				},
			}

			mgr := exporter.NewManager[exporter.FeatureEvent](
				dataExporterMock,
				exporter.DefaultExporterCleanQueueInterval,
				nil,
			)

			go mgr.Start()
			defer mgr.Stop()

			// Initialize inputEvents slice
			inputEvents := make([]exporter.FeatureEvent, 0, 101)
			for range 101 {
				inputEvents = append(inputEvents, exporter.NewFeatureEvent(
					ffcontext.NewEvaluationContextBuilder(aBCD).
						AddCustom(anonymous, true).
						Build(),
					randomKey,
					"YO",
					defaultVar2,
					false,
					"",
					sERVER,
					nil,
				))
			}

			want := make([]exporter.ExportableEvent, len(inputEvents))
			for i, event := range inputEvents {
				mgr.AddEvent(event)
				want[i] = event
			}

			assert.Len(t, tt.mockExporter.GetExportedEvents(), tt.wantExported)
			assert.Equal(t, want[:tt.wantExported], tt.mockExporter.GetExportedEvents())
		})
	}
}

func TestDataExporterManager_defaultFlush(t *testing.T) {
	t.Parallel()

	tests := []struct {
		mockExporter mock.ExporterMock
		name         string
		wantExported int
	}{
		{
			name:         "classic exporter",
			mockExporter: &mock.Exporter{Bulk: true},
			wantExported: 100000,
		},
		{
			name:         deprecatedExporter,
			mockExporter: &mock.ExporterDeprecated{Bulk: true},
			wantExported: 100000,
		},
		{
			name:         "deprecated exporter v2",
			mockExporter: &mock.ExporterDeprecatedV2{Bulk: true},
			wantExported: 100000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dataExporterMock := []exporter.Config{
				{
					FlushInterval:    0,
					MaxEventInMemory: 0,
					Exporter:         tt.mockExporter,
				},
			}

			mgr := exporter.NewManager[exporter.FeatureEvent](
				dataExporterMock, exporter.DefaultExporterCleanQueueInterval, nil)

			go mgr.Start()
			defer mgr.Stop()

			// Initialize inputEvents slice
			inputEvents := make([]exporter.FeatureEvent, 0, 100001)
			for range 100001 {
				inputEvents = append(inputEvents, exporter.NewFeatureEvent(
					ffcontext.NewEvaluationContextBuilder(aBCD).
						AddCustom(anonymous, true).
						Build(),
					randomKey,
					"YO",
					defaultVar2,
					false,
					"",
					sERVER,
					nil,
				))
			}

			want := make([]exporter.ExportableEvent, len(inputEvents))
			for i, event := range inputEvents {
				mgr.AddEvent(event)
				want[i] = event
			}

			assert.Len(t, tt.mockExporter.GetExportedEvents(), tt.wantExported)
			assert.Equal(t, want[:tt.wantExported], tt.mockExporter.GetExportedEvents())
		})
	}
}

func TestDataExporterManager_exporterReturnError(t *testing.T) {
	t.Parallel()

	mockExporter := mock.Exporter{Err: errRandom, ExpectedNumberErr: 1, Bulk: true}
	dataExporterMock := []exporter.Config{
		{
			FlushInterval:    10 * time.Minute,
			MaxEventInMemory: 100,
			Exporter:         &mockExporter,
		},
	}

	file, _ := os.CreateTemp(t.TempDir(), "log")
	defer func() { _ = file.Close() }()
	defer func() { _ = os.Remove(file.Name()) }()

	handler := slogassert.New(t, slog.LevelInfo, nil)
	logger := slog.New(handler)

	mgr := exporter.NewManager[exporter.FeatureEvent](dataExporterMock,
		exporter.DefaultExporterCleanQueueInterval, &fflog.FFLogger{LeveledLogger: logger})

	go mgr.Start()
	defer mgr.Stop()

	// Initialize inputEvents slice
	inputEvents := make([]exporter.FeatureEvent, 0, 201)
	for range 201 {
		inputEvents = append(inputEvents, exporter.NewFeatureEvent(
			ffcontext.NewEvaluationContextBuilder(aBCD).AddCustom(anonymous, true).Build(),
			randomKey, "YO", defaultVar2, false, "", sERVER, nil))
	}

	want := make([]exporter.ExportableEvent, len(inputEvents))
	for i, event := range inputEvents {
		mgr.AddEvent(event)
		want[i] = event
	}
	// check that the first 100 events are exported
	assert.Equal(t, want[:100], mockExporter.GetExportedEvents()[:100])
	handler.AssertMessage("error while exporting data: random err")
}

func TestDataExporterManager_nonBulkExporter(t *testing.T) {
	t.Parallel()

	mockExporter := mock.Exporter{Bulk: false}
	dataExporterMock := []exporter.Config{
		{
			FlushInterval:    0,
			MaxEventInMemory: 0,
			Exporter:         &mockExporter,
		},
	}

	mgr := exporter.NewManager[exporter.FeatureEvent](
		dataExporterMock, exporter.DefaultExporterCleanQueueInterval, nil)
	defer mgr.Stop()

	// Initialize inputEvents slice
	inputEvents := make([]exporter.FeatureEvent, 0, 100)
	for range 100 {
		inputEvents = append(inputEvents, exporter.NewFeatureEvent(
			ffcontext.NewEvaluationContextBuilder(aBCD).AddCustom(anonymous, true).Build(),
			randomKey, "YO", defaultVar2, false, "", sERVER, nil))
	}

	want := make([]exporter.ExportableEvent, len(inputEvents))
	for i, event := range inputEvents {
		mgr.AddEvent(event)
		want[i] = event
		// we have to wait because we are opening a new thread to slow down the flag evaluation.
		time.Sleep(1 * time.Millisecond)
	}

	assert.Equal(t, want[:100], mockExporter.GetExportedEvents())
}

func TestAddExporterMetadataFromContextToExporter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		ctx  ffcontext.EvaluationContext
		want map[string]any
		name string
	}{
		{
			name: "extract exporter metadata from context",
			ctx: ffcontext.NewEvaluationContextBuilder("targeting-key").
				AddCustom("gofeatureflag", map[string]any{
					"exporterMetadata": map[string]any{
						"key1": "value1",
						"key2": 123,
						"key3": true,
						"key4": 123.45,
					},
				}).
				Build(),
			want: map[string]any{
				"key1": "value1",
				"key2": 123,
				"key3": true,
				"key4": 123.45,
			},
		},
		{
			name: "no exporter metadata in the context",
			ctx:  ffcontext.NewEvaluationContextBuilder("targeting-key").Build(),
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mockExporter := &mock.Exporter{}
			config := ffclient.Config{
				Retriever: &fileretriever.Retriever{Path: "../testdata/flag-config.yaml"},
				DataExporter: ffclient.DataExporter{
					Exporter:      mockExporter,
					FlushInterval: 100 * time.Millisecond,
				},
			}
			goff, err := ffclient.New(config)
			require.NoError(t, err)

			defer goff.Close()

			_, err = goff.BoolVariation(testFlag, tt.ctx, false)
			require.NoError(t, err)

			// The event is collected in a goroutine and exported by the flush ticker, so we
			// can't rely on a fixed sleep: a loaded CI runner can stall past the tick.
			require.Eventually(t, func() bool {
				return len(mockExporter.GetExportedEvents()) == 1
			}, 10*time.Second, 10*time.Millisecond,
				"the flush ticker never exported the evaluation event")

			switch val := mockExporter.GetExportedEvents()[0].(type) {
			case exporter.FeatureEvent:
				assert.Equal(t, tt.want, val.Metadata)
			default:
				assert.Fail(t, "The exported event is not a FeatureEvent")
			}
		})
	}
}

func TestDataExporterManager_multipleExporters(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("timing-sensitive: Windows timer granularity (~15ms) breaks the sub-millisecond " +
			"AddEvent/flush-interval assumptions this test relies on")
	}

	mockExporter1 := mock.Exporter{Bulk: false}
	mockExporter2 := mock.Exporter{Bulk: true}
	dataExporterMock := []exporter.Config{
		{
			FlushInterval:    0,
			MaxEventInMemory: 0,
			Exporter:         &mockExporter1,
		},
		{
			FlushInterval:    200 * time.Millisecond,
			MaxEventInMemory: 200,
			Exporter:         &mockExporter2,
		},
	}

	mgr := exporter.NewManager[exporter.FeatureEvent](
		dataExporterMock, exporter.DefaultExporterCleanQueueInterval, nil)

	go mgr.Start()
	defer mgr.Stop()

	// Initialize inputEvents slice
	inputEvents := make([]exporter.FeatureEvent, 0, 100)
	for range 100 {
		inputEvents = append(inputEvents, exporter.NewFeatureEvent(
			ffcontext.NewEvaluationContextBuilder(aBCD).AddCustom(anonymous, true).Build(),
			randomKey, "YO", defaultVar2, false, "", sERVER, nil))
	}

	want := make([]exporter.ExportableEvent, len(inputEvents))
	for i, event := range inputEvents {
		mgr.AddEvent(event)
		want[i] = event
		// we have to wait because we are opening a new thread to slow down the flag evaluation.
		time.Sleep(1 * time.Millisecond)
	}

	assert.Equal(t, want[:100], mockExporter1.GetExportedEvents())
	assert.Empty(t, mockExporter2.GetExportedEvents())
	time.Sleep(250 * time.Millisecond)
	assert.Equal(t, want[:100], mockExporter2.GetExportedEvents())
}

func TestDataExporterManagerMultipleExportersWithDifferentFlushInterval(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("timing-sensitive: Windows timer granularity (~15ms) breaks the sub-millisecond " +
			"AddEvent/flush-interval assumptions this test relies on")
	}

	mockExporter1 := mock.Exporter{Bulk: true}
	mockExporter2 := mock.Exporter{Bulk: true}
	dataExporterMock := []exporter.Config{
		{
			FlushInterval:    50 * time.Millisecond,
			MaxEventInMemory: 0,
			Exporter:         &mockExporter1,
		},
		{
			FlushInterval:    0 * time.Millisecond,
			MaxEventInMemory: 100,
			Exporter:         &mockExporter2,
		},
	}

	mgr := exporter.NewManager[exporter.FeatureEvent](
		dataExporterMock, exporter.DefaultExporterCleanQueueInterval, nil)

	go mgr.Start()
	defer mgr.Stop()

	// Initialize inputEvents slice
	inputEvents := make([]exporter.FeatureEvent, 0, 100)
	for range 100 {
		inputEvents = append(inputEvents, exporter.NewFeatureEvent(
			ffcontext.NewEvaluationContextBuilder(aBCD).AddCustom(anonymous, true).Build(),
			randomKey, "YO", defaultVar2, false, "", sERVER, nil))
	}

	go func(mgr exporter.Manager[exporter.FeatureEvent]) {
		for _, event := range inputEvents {
			mgr.AddEvent(event)
			// we have to wait because we are opening a new thread to slow down the flag evaluation.
			time.Sleep(1 * time.Millisecond)
		}
	}(mgr)

	assert.Empty(t, mockExporter2.GetExportedEvents())
	assert.Empty(t, mockExporter1.GetExportedEvents())
	time.Sleep(70 * time.Millisecond)
	assert.NotEmpty(t, mockExporter1.GetExportedEvents())
	assert.Empty(t, mockExporter2.GetExportedEvents())
	time.Sleep(200 * time.Millisecond)
	assert.NotEmpty(t, mockExporter1.GetExportedEvents())
	assert.NotEmpty(t, mockExporter2.GetExportedEvents())
}

func TestDataExporterManager_ValidateNumberOfEvents(t *testing.T) {
	t.Parallel()

	// We are running the same test multiple times to have more chance to have a race condition.
	for i := 1; i < 20; i++ {
		t.Run(fmt.Sprintf("ValidateNumberOfEvents #%d", i), func(t *testing.T) {
			t.Parallel()

			mockExporter := mock.Exporter{Bulk: true}
			// Init ffclient with a file retriever.
			client, err := ffclient.New(ffclient.Config{
				PollingInterval: 10 * time.Second,
				LeveledLogger:   slog.Default(),
				Retriever: &fileretriever.Retriever{
					Path: "../testdata/flag-config.yaml",
				},
				DataExporter: ffclient.DataExporter{
					FlushInterval:    150 * time.Millisecond,
					MaxEventInMemory: 100,
					Exporter:         &mockExporter,
				},
			})
			require.NoError(t, err)

			// create users
			user1 := ffcontext.
				NewEvaluationContextBuilder("aea2fdc1-b9a0-417a-b707-0c9083de68e3").
				AddCustom(anonymous, true).
				Build()
			user2 := ffcontext.NewEvaluationContext("332460b9-a8aa-4f7a-bc5d-9cc33632df9a")

			_, _ = client.BoolVariation(testFlag, user1, false)
			_, _ = client.BoolVariation(testFlag, user2, false)
			_, _ = client.StringVariation("test-flag2", user1, "defaultValue")
			_, _ = client.JSONVariation(
				"test-flag2",
				user1,
				map[string]any{"test": toto},
			)
			// Events are collected in a goroutine (see notifyVariation) and exported by the
			// flush ticker. A fixed sleep only gives us one or two ticks of margin, so a CI
			// stall (very common on the Windows runner) makes every tick fire before the
			// events reach the event store, and we export nothing.
			require.Eventually(t, func() bool {
				return len(mockExporter.GetExportedEvents()) == 4
			}, 10*time.Second, 10*time.Millisecond,
				"the flush ticker never exported the 4 evaluation events")

			// Close() waits for the in-flight collect goroutines and then flushes, so the last
			// 2 events are exported synchronously and this assertion needs no waiting.
			_, _ = client.BoolVariation(testFlag, user1, false)
			_, _ = client.BoolVariation(testFlag, user2, false)
			client.Close() // a flush is triggered here
			assert.Len(t, mockExporter.GetExportedEvents(), 6)
		})
	}
}

// goroutinesLabeled counts the goroutines that carry the pprof label key=value. The goroutines started by
// code that runs under [pprof.Do] inherit its labels, so the count is scoped to what one test started
// and does not depend on the other tests running in parallel.
func goroutinesLabeled(key, value string) int {
	var buf bytes.Buffer

	err := pprof.Lookup("goroutine").WriteTo(&buf, 1)
	if err != nil {
		return -1
	}

	want := fmt.Sprintf("%q:%q", key, value)
	total, current := 0, 0

	for line := range strings.SplitSeq(buf.String(), "\n") {
		if n, _, ok := strings.Cut(line, " @ "); ok {
			v, parseErr := strconv.Atoi(n)
			if parseErr == nil {
				current = v
			}

			continue
		}

		if strings.HasPrefix(line, "# labels:") && strings.Contains(line, want) {
			total += current
		}
	}

	return total
}

// TestDataExporterManager_StopReleasesEverything checks the two things Stop() has to guarantee:
// it is safe to call several times, since callers are free to Close() a client twice, and it
// releases every goroutine it started, including the event store cleaner. Forgetting the store
// leaks one goroutine and the events it retains per client, which is unbounded for the relay
// proxy since it builds a client per flagset on every hot reload. The goroutines are counted by
// pprof label, so only the ones of the managers of the test are looked at.
func TestDataExporterManager_StopReleasesEverything(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		nbManagers    int
		wantRunning   int
		wantStopTwice bool
	}{
		{name: "one manager", nbManagers: 1, wantRunning: 0, wantStopTwice: true},
		{name: "one manager stopped once", nbManagers: 1, wantRunning: 0, wantStopTwice: false},
		{name: "many managers", nbManagers: 50, wantRunning: 0, wantStopTwice: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			label := t.Name()
			started := 0

			pprof.Do(t.Context(), pprof.Labels(leakcheck, label), func(context.Context) {
				for range tt.nbManagers {
					mgr := exporter.NewManager[exporter.FeatureEvent](
						[]exporter.Config{
							{
								FlushInterval:    10 * time.Millisecond,
								MaxEventInMemory: 100,
								Exporter:         &mock.Exporter{Bulk: true},
							},
						},
						10*time.Millisecond,
						nil,
					)
					mgr.Start()
					mgr.AddEvent(exporter.NewFeatureEvent(
						ffcontext.NewEvaluationContextBuilder(aBCD).Build(),
						randomKey, "YO", defaultVar2, false, "", sERVER, nil))

					started = max(started, goroutinesLabeled(leakcheck, label))

					mgr.Stop()
					// Close() can legitimately be called twice, and Stop() used to panic with
					// "close of closed channel" on the second call.
					if tt.wantStopTwice {
						assert.NotPanics(t, mgr.Stop)
					}
				}
			})

			assert.Positive(t, started, "the managers must have started goroutines, or the check proves nothing")
			require.Eventually(t, func() bool {
				return goroutinesLabeled(leakcheck, label) == tt.wantRunning
			}, 10*time.Second, 20*time.Millisecond,
				"Stop() leaked goroutines of the %d managers of the test", tt.nbManagers)
		})
	}
}

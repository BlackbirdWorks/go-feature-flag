package events

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/exporter"
)

// stopGateManager is a manager whose Stop signals that it was called and then waits for a gate.
type stopGateManager struct {
	gate    chan struct{}
	entered chan struct{}
}

func (*stopGateManager) AddEvent(Event) {}
func (*stopGateManager) Start()         {}

func (s *stopGateManager) Stop() {
	if s.entered != nil {
		s.entered <- struct{}{}
	}

	if s.gate != nil {
		<-s.gate
	}
}

// reloadDoc is a document with one log exporter and one slack notifier, told apart by the format and the URL.
func reloadDoc(generation string) string {
	return "version: 2\nexporters:\n  e: {kind: log, logFormat: gen-" + generation + "}\n" +
		"notifiers:\n  n: {kind: slack, webhookUrl: {fromEnv: URL_" + generation + "}}\n"
}

func reloadOptions() bootstrap.LoadOptions {
	return bootstrap.LoadOptions{LookupEnv: func(name string) (string, bool) {
		if strings.HasPrefix(name, "URL_") {
			return "https://hook.example/" + name, true
		}

		return "", false
	}}
}

// TestRuntimeOverlappingReloadsLeaveOneConfiguration starts reload A, which waits for the drain of the old
// exporter (its Stop is gated), and runs reload B to the end meanwhile. When A resumes, the exporters and the
// notifiers are those of B, not a mix: both sets of a reload are published in one operation.
func TestRuntimeOverlappingReloadsLeaveOneConfiguration(t *testing.T) {
	t.Parallel()

	opts := reloadOptions()
	gate := make(chan struct{})
	entered := make(chan struct{}, 4)

	var (
		mu    sync.Mutex
		built int
	)

	// only the first manager (generation 0) has a Stop that waits
	newManager := func(_ string, _ exporter.Config, _ *slog.Logger) FeatureManager {
		mu.Lock()
		defer mu.Unlock()

		built++

		if built == 1 {
			return &stopGateManager{gate: gate, entered: entered}
		}

		return &loggingManager{log: &orderLog{}}
	}

	runtime := NewRuntime(&RuntimeConfig{NewManager: newManager, Builder: Builder{Secrets: opts}})
	require.NoError(t, runtime.Start(context.Background(), nil))

	t.Cleanup(func() { _, _ = runtime.Shutdown(context.Background()) })

	specsOf := func(generation string) Specs {
		cfg, err := bootstrap.Parse([]byte(reloadDoc(generation)), nil, opts)
		require.NoError(t, err)

		return SpecsFrom(cfg)
	}

	_, err := runtime.Reload(context.Background(), specsOf("0"))
	require.NoError(t, err)

	reloadedA := make(chan struct{})

	go func() {
		defer close(reloadedA)

		_, reloadErr := runtime.Reload(context.Background(), specsOf("1"))
		assert.NoError(t, reloadErr)
	}()

	// A published its sets and waits for the old exporter, whose Stop is gated
	<-entered

	_, err = runtime.Reload(context.Background(), specsOf("2"))
	require.NoError(t, err)

	close(gate)
	<-reloadedA

	wantExporters, err := Builder{Secrets: opts}.Exporters(context.Background(), specsOf("2"))
	require.NoError(t, err)

	wantNotifiers, err := Builder{Secrets: opts}.Notifiers(context.Background(), specsOf("2"))
	require.NoError(t, err)

	gotExporters := runtime.Dispatcher.state.Load().active
	gotNotifiers := runtime.Notifications.state.Load().active

	require.Len(t, gotExporters, 1)
	require.Len(t, gotNotifiers, 1)
	assert.Equal(t, wantExporters[0].Identity, gotExporters[0].def.Identity, "the exporter of reload B")
	assert.Equal(t, wantNotifiers[0].Identity, gotNotifiers[0].def.Identity, "the notifier of reload B")
}

// closerExporter is an engine exporter that holds a resource.
type closerExporter struct {
	log *orderLog
	err error
}

func (closerExporter) IsBulk() bool { return true }

func (closerExporter) Export(context.Context, *slog.Logger, []exporter.ExportableEvent) error {
	return nil
}

func (c closerExporter) Close() error {
	c.log.add("close")

	return c.err
}

// loggingManager records when it is stopped.
type loggingManager struct {
	log *orderLog
}

func (*loggingManager) AddEvent(Event) {}
func (*loggingManager) Start()         {}
func (l *loggingManager) Stop()        { l.log.add("stop") }

// TestInstanceReleasesItsExporterAfterTheFinalFlush checks that an exporter that holds resources is closed once
// its manager stopped (which flushes it), and that an exporter without any is left alone.
func TestInstanceReleasesItsExporterAfterTheFinalFlush(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		exporter  func(log *orderLog) exporter.Exporter
		wantOrder []string
	}{
		{
			name: "an exporter with a Close", wantOrder: []string{"stop", "close"},
			exporter: func(log *orderLog) exporter.Exporter { return closerExporter{log: log} },
		},
		{
			name: "an exporter without one", wantOrder: []string{"stop"},
			exporter: func(*orderLog) exporter.Exporter { return echoExporter{} },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			log := &orderLog{}
			def := NewExporterDef("a", "file", "v1", tt.exporter(log), exporter.Config{})
			entry := &nameEntry{name: "a"}
			def.wrapper().name = &entry.stats

			inst := newManaged(&def, &loggingManager{log: log}, entry, 8, 8, slog.New(slog.DiscardHandler))

			var background sync.WaitGroup

			inst.startClose(context.Background(), &background)
			background.Wait()

			log.mu.Lock()
			defer log.mu.Unlock()

			assert.Equal(t, tt.wantOrder, log.order)
		})
	}
}

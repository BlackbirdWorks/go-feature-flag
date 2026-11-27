package events

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/notifier"
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
	gate := newGate(t)
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

	shutdownOnCleanup(t, runtime)

	specsOf := func(generation string) Specs {
		cfg, err := bootstrap.Parse([]byte(reloadDoc(generation)), nil, opts)
		require.NoError(t, err)

		return SpecsFrom(cfg)
	}

	_, err := runtime.Reload(context.Background(), specsOf("0"))
	require.NoError(t, err)

	reloadedA := make(chan struct{})
	specsA := specsOf("1") // built here: the goroutine below must not call require

	go func() {
		defer close(reloadedA)

		_, reloadErr := runtime.Reload(context.Background(), specsA)
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

			inst.startClose(context.Background())
			<-inst.finished

			log.mu.Lock()
			defer log.mu.Unlock()

			assert.Equal(t, tt.wantOrder, log.order)
		})
	}
}

// newManagerWithStopGate returns a manager constructor whose first manager has a Stop that waits for the gate.
func newManagerWithStopGate(gate, entered chan struct{}) NewManagerFunc {
	var (
		mu    sync.Mutex
		built int
	)

	return func(string, exporter.Config, *slog.Logger) FeatureManager {
		mu.Lock()
		defer mu.Unlock()

		built++

		if built == 1 && gate != nil {
			return &stopGateManager{gate: gate, entered: entered}
		}

		return &loggingManager{log: &orderLog{}}
	}
}

// TestRuntimeReloadDrainsRunAtTheSameTime replaces an exporter and a notifier together. Each side has a deadline of
// its own context, created when the sets are published, and the two waits run at the same time: the deadline of
// the notifier cuts the old notifier and frees its replacement while the exporter, whose own deadline has not
// passed, is still being waited for. A reload that waited for the exporter first could not do this.
func TestRuntimeReloadDrainsRunAtTheSameTime(t *testing.T) {
	t.Parallel()

	tests := []struct {
		exporterGate     chan struct{}
		notifierGate     chan struct{}
		name             string
		wantCutExporters []string
		wantCutNotifiers []string
	}{
		{
			name: "the old exporter and the old notifier are stuck", exporterGate: make(chan struct{}),
			notifierGate: make(chan struct{}), wantCutExporters: []string{"e"}, wantCutNotifiers: []string{"n"},
		},
		{
			name: "only the old exporter is stuck", exporterGate: make(chan struct{}), notifierGate: nil,
			wantCutExporters: []string{"e"}, wantCutNotifiers: nil,
		},
		{
			name: "only the old notifier is stuck", exporterGate: nil, notifierGate: make(chan struct{}),
			wantCutExporters: nil, wantCutNotifiers: []string{"n"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			stopEntered := make(chan struct{}, 1)
			runtime := NewRuntime(&RuntimeConfig{NewManager: newManagerWithStopGate(tt.exporterGate, stopEntered)})

			expCancel, notCancel := make(chan context.CancelFunc, 1), make(chan context.CancelFunc, 1)
			hook := func(out chan context.CancelFunc) func(context.Context) (context.Context, context.CancelFunc) {
				return func(parent context.Context) (context.Context, context.CancelFunc) {
					ctx, cancel := context.WithCancel(context.WithoutCancel(parent))
					out <- cancel

					return ctx, cancel
				}
			}

			changes := make(chan flagruntime.Change)
			require.NoError(t, runtime.Start(context.Background(), changes))
			shutdownOnCleanup(t, runtime)

			// the gates are opened when the test ends, whatever its outcome. The cleanups run in the reverse order,
			// so they are opened before the runtime is shut down. Nothing can fail between the runtime and this line.
			openOnCleanup(t, tt.exporterGate, tt.notifierGate)

			log := &orderLog{}
			oldN := &gatedNotifier{gate: tt.notifierGate, entered: make(chan struct{}, 1), label: "old-", log: log}
			newN := &gatedNotifier{got: make(chan struct{}, 4), label: "new-", log: log}

			exporterDef := func(identity string) []ExporterDef {
				return []ExporterDef{NewExporterDef("e", "file", identity, echoExporter{}, exporter.Config{})}
			}
			notifierDef := func(identity string, notifier notifier.Notifier) []NotifierDef {
				return []NotifierDef{NewNotifierDef("n", "webhook", identity, notifier)}
			}

			_, err := runtime.apply(context.Background(), exporterDef("v1"), notifierDef("v1", oldN))
			require.NoError(t, err)

			if tt.notifierGate != nil {
				changes <- addedChange("0")

				await(t, oldN.entered, "the old notifier to take its change")
			}

			runtime.Dispatcher.cfg.drainContext = hook(expCancel)
			runtime.Notifications.cfg.drainContext = hook(notCancel)

			type result struct {
				err    error
				report ReloadReport
			}

			applied := make(chan result, 1)

			go func() {
				report, applyErr := runtime.apply(context.Background(), exporterDef("v2"), notifierDef("v2", newN))
				applied <- result{report: report, err: applyErr}
			}()

			// both deadlines exist before any wait ends: the waits started together
			cancelExporter := awaitCancel(t, expCancel)
			cancelNotifier := awaitCancel(t, notCancel)

			// the deadline of the notifier passes (a notifier that is not stuck ends by itself): the old notifier is
			// cut, its replacement receives a change
			if tt.notifierGate != nil {
				cancelNotifier()
			}

			changes <- addedChange("1")

			await(t, newN.got, "the replacement notifier to receive a change")

			if tt.exporterGate != nil {
				await(t, stopEntered, "the old exporter to be stopped")

				select {
				case <-applied:
					require.FailNow(t, "the reload returned while the deadline of the exporter had not passed")
				default:
				}
			}

			if tt.exporterGate != nil {
				cancelExporter()
			}

			var got result

			select {
			case got = <-applied:
			case <-time.After(20 * time.Second):
				require.FailNow(t, "timed out waiting for the reload")
			}

			require.NoError(t, got.err)
			assert.Equal(t, tt.wantCutExporters, got.report.Exporters.Cut)
			assert.Equal(t, tt.wantCutNotifiers, got.report.Notifiers.Cut)

			if tt.exporterGate != nil {
				close(tt.exporterGate)
			}

			if tt.notifierGate != nil {
				close(tt.notifierGate)
			}
		})
	}
}

func awaitCancel(t *testing.T, ch <-chan context.CancelFunc) context.CancelFunc {
	t.Helper()

	select {
	case cancel := <-ch:
		return cancel
	case <-time.After(20 * time.Second):
		require.FailNow(t, "timed out waiting for the deadline of a drain to be created")

		return nil
	}
}

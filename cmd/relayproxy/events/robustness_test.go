package events_test

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/events"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/exporter"
)

// panicExporter panics, with a value that carries a secret, each time it exports.
type panicExporter struct {
	called chan struct{}
	calls  atomic.Int64
}

func (*panicExporter) IsBulk() bool { return true }

func (p *panicExporter) Export(context.Context, *slog.Logger, []exporter.ExportableEvent) error {
	p.calls.Add(1)

	select {
	case p.called <- struct{}{}:
	default:
	}

	panic("CANARY-PANIC-VALUE: https://hook.example/CANARY-PANIC-TOKEN")
}

// TestExporterPanicIsRecoveredByTheEngineManager runs an exporter that panics under the real engine manager,
// which flushes in a goroutine of its own where a panic would end the process: the flush is treated as failed,
// counted as the class panic, and logged once with the type of the value only.
func TestExporterPanicIsRecoveredByTheEngineManager(t *testing.T) {
	t.Parallel()

	rec, lines := newLogRecorder()
	sink := &panicExporter{called: make(chan struct{}, 8)}

	newManager, tap := tapped()
	disp := events.NewDispatcher(&events.DispatcherConfig{NewManager: newManager, Logger: slog.New(rec)})
	require.NoError(t, disp.Start(context.Background()))

	_, err := disp.Apply(context.Background(), []events.ExporterDef{
		// one event is enough to start a flush
		events.NewExporterDef("a", "file", "v1", sink, exporter.Config{FlushInterval: time.Hour, MaxEventInMemory: 1}),
	})
	require.NoError(t, err)

	require.True(t, disp.Collect(ev(0)))
	await(t, tap, "the event to reach the manager")
	await(t, sink.called, "the first flush")

	require.True(t, disp.Collect(ev(1)))
	await(t, tap, "the second event to reach the manager")

	report, err := disp.Shutdown(bounded(t))
	require.NoError(t, err)

	status := disp.Status()
	got := statusOf(t, &status, "a")

	assert.GreaterOrEqual(t, sink.calls.Load(), int64(2), "the exporter was called by the flush and by the final flush")
	assert.Equal(t, uint64(sink.calls.Load()), got.FlushFailures, "every call failed")
	assert.Equal(t, events.ErrorPanic, got.LastError)
	assert.Zero(t, got.Exported)
	assert.Equal(t, uint64(2), got.Dropped.Drain, "the events no flush could deliver")
	assert.Equal(t, int64(2), report.Undelivered)
	assertExporterAccounting(t, &status, "after the panics")

	var panics []string

	all := strings.Join(lines(), "\n")

	for _, line := range lines() {
		if strings.Contains(line, "the flush is treated as failed") {
			panics = append(panics, line)
		}
	}

	require.Len(t, panics, 1, "logged once per instance, though the exporter panicked at least twice")
	assert.Contains(t, panics[0], "type=string")
	assert.NotContains(t, all, "CANARY-PANIC", "the value of a panic can carry a secret")
}

// TestDefinitionsPrintNoSecret checks that a definition, or the exporter or notifier it holds, prints the
// redaction marker or a summary with every verb and as a slog value.
func TestDefinitionsPrintNoSecret(t *testing.T) {
	t.Parallel()

	cfg, opts := parseDoc(t, connections+`exporters:
  hook: {kind: webhook, endpointUrl: {fromEnv: URL}, secret: {fromEnv: SECRET}}
notifiers:
  chat: {kind: slack, webhookUrl: {fromEnv: URL}}
`, map[string]string{envURL: "https://hook.example/CANARY-URL-0001", envSecret: "CANARY-SECRET-0002"})

	exporters, err := events.Builder{Secrets: opts}.Exporters(context.Background(), events.SpecsFrom(cfg))
	require.NoError(t, err)

	notifiers, err := events.Builder{Secrets: opts}.Notifiers(context.Background(), events.SpecsFrom(cfg))
	require.NoError(t, err)

	values := map[string]any{
		"exporter def":         exporters[0],
		"exporter def pointer": &exporters[0],
		"exporter wrapper":     exporters[0].Config.Exporter,
		"exporter inner":       exporters[0].Inner,
		"exporter config":      exporters[0].Config,
		"notifier def":         notifiers[0],
		"notifier def pointer": &notifiers[0],
		"notifier inner":       notifiers[0].Notifier,
		"slice of defs":        exporters,
	}

	for name, value := range values {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			for _, format := range []string{"%v", "%+v", "%#v", "%s"} {
				out := fmt.Sprintf(format, value)

				assert.NotContains(t, out, "CANARY", "%s of %s", format, name)
			}

			var sb strings.Builder

			logger := slog.New(slog.NewTextHandler(&sb, nil))
			logger.LogAttrs(context.Background(), slog.LevelInfo, "value", slog.Any("v", value))
			assert.NotContains(t, sb.String(), "CANARY", "slog of %s", name)
		})
	}

	assert.Equal(t, "ExporterDef{name=hook kind=webhook identity="+exporters[0].Identity[:8]+"}", exporters[0].String())
	assert.Equal(t, exporters[0].String(), fmt.Sprintf("%#v", &exporters[0]))
	assert.NotContains(t, fmt.Sprintf("%+v", exporters[0]), "CANARY", "printed by value")
	assert.Equal(t, "NotifierDef{name=chat kind=slack identity="+notifiers[0].Identity[:8]+"}", notifiers[0].String())
	assert.Equal(t, bootstrap.RedactedValue, exporters[0].Inner.String())
}

// TestRuntimeReloadIsAtomicAcrossBothSides closes the notifier side and reloads: nothing changes on the exporter
// side either.
func TestRuntimeReloadIsAtomicAcrossBothSides(t *testing.T) {
	t.Parallel()

	cfg, opts := parse(t, "exporters:\n  e: {kind: log}\nnotifiers:\n  n: {kind: slack, webhookUrl: {fromEnv: URL}}\n")
	runtime := events.NewRuntime(&events.RuntimeConfig{Builder: events.Builder{Secrets: opts}})

	require.NoError(t, runtime.Start(context.Background(), nil))
	t.Cleanup(func() { _, _ = runtime.Shutdown(canceled()) })

	// only the notifier side is closed
	require.NoError(t, runtime.Notifications.Shutdown(bounded(t)))

	_, err := runtime.Reload(context.Background(), events.SpecsFrom(cfg))
	require.ErrorIs(t, err, events.ErrClosed)
	assert.Empty(t, runtime.Status().Exporters, "the exporter side was not changed")
	assert.False(t, runtime.Collect(ev(1)), "and still has no exporter")
}

// TestRuntimeContextEndsBothSides checks that the end of the context given to Start shuts both sides down.
func TestRuntimeContextEndsBothSides(t *testing.T) {
	t.Parallel()

	runtime := events.NewRuntime(&events.RuntimeConfig{})
	ctx, cancel := context.WithCancel(context.Background())

	require.NoError(t, runtime.Start(ctx, nil), "a nil channel: no changes are consumed, the notifications still end")

	cancel()
	await(t, runtime.Dispatcher.Done(), "the dispatcher to shut down")
	await(t, runtime.Notifications.Done(), "the notifications to shut down")

	_, err := runtime.Reload(context.Background(), events.Specs{})
	require.ErrorIs(t, err, events.ErrClosed)
}

func TestDropsTotal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		drops events.Drops
		want  uint64
	}{
		{name: "none", want: 0},
		{
			name:  "one of each",
			drops: events.Drops{QueueFull: 1, Shutdown: 2, NoExporter: 4, Overflow: 8, Drain: 16},
			want:  31,
		},
		{name: "one reason", drops: events.Drops{Overflow: 7}, want: 7},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, tt.drops.Total())
		})
	}
}

// TestFlushDurationIsMeasuredWithTheClock checks the duration of a flush against the stepping clock of the
// package: every flush lasts a positive whole number of steps.
func TestFlushDurationIsMeasuredWithTheClock(t *testing.T) {
	t.Parallel()

	sink := &nopExporter{}
	disp := events.NewDispatcher(&events.DispatcherConfig{})
	require.NoError(t, disp.Start(context.Background()))

	_, err := disp.Apply(context.Background(), []events.ExporterDef{
		events.NewExporterDef(
			"a",
			"file",
			"v1",
			sink,
			exporter.Config{FlushInterval: time.Hour, MaxEventInMemory: 1000},
		),
	})
	require.NoError(t, err)

	require.True(t, disp.Collect(ev(1)))

	_, err = disp.Shutdown(bounded(t))
	require.NoError(t, err)

	status := disp.Status()
	got := statusOf(t, &status, "a")

	require.Equal(t, uint64(1), got.Flushes)
	assert.GreaterOrEqual(t, got.FlushSeconds, stepSeconds, "a flush lasts at least one step of the clock")
	assert.InDelta(t, 0, got.FlushSeconds-float64(int(got.FlushSeconds/stepSeconds))*stepSeconds, 1e-9,
		"and a whole number of steps")
}

// TestRuntimeShutdownClosesHealthyExportersWhileANotifierIsStuck checks that the two sides shut down at the same
// time under one deadline: a notifier that holds its deadline does not leave the exporters with a deadline that has
// passed, so a healthy exporter is closed, not cut.
func TestRuntimeShutdownClosesHealthyExportersWhileANotifierIsStuck(t *testing.T) {
	t.Parallel()

	fac := newFactory()
	fac.stopEntered = make(chan struct{}, 1)

	runtime := events.NewRuntime(&events.RuntimeConfig{NewManager: fac.build})
	changes := make(chan flagruntime.Change)

	require.NoError(t, runtime.Start(context.Background(), changes))

	stuck := &recordingNotifier{gate: make(chan struct{}), entered: make(chan struct{}, 1)}

	_, err := runtime.Notifications.Apply(context.Background(), []events.NotifierDef{ndef("n", "v1", stuck)})
	require.NoError(t, err)

	_, err = runtime.Dispatcher.Apply(context.Background(), defs("a"))
	require.NoError(t, err)

	changes <- change("set", 1)

	await(t, stuck.entered, "the notifier to take its change")

	for i := range 5 {
		require.True(t, runtime.Collect(ev(i)))
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	finished := make(chan events.ShutdownReport, 1)

	go func() {
		report, _ := runtime.Shutdown(ctx)
		finished <- report
	}()

	// the exporter is being closed while the notifier still holds its call, and its close completes
	await(t, fac.stopEntered, "the exporter to be closed while the notifier is stuck")
	require.NoError(t, runtime.Dispatcher.WaitDrains(bounded(t)))

	// only now does the deadline pass
	cancel()

	report := await(t, finished, "the shutdown")

	close(stuck.gate)

	assert.Equal(t, []events.DrainResult{{Name: "a", Kind: "file", Undelivered: 0, Cut: false}}, report.Drained)
	assert.Equal(t, []string{"flag-0", eventOneKey, eventTwoKey, eventThreeKey, "flag-4"}, keys(fac.of("a", 0).got()))
	assert.Equal(t, events.Drops{}, report.Dropped)
}

package events_test

import (
	"context"
	"fmt"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/events"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/exporter"
)

const (
	canaryURL    = "CANARY-URL-9f3a"
	canarySecret = "CANARY-SECRET-77c1"
	canaryHeader = "CANARY-HEADER-5b2e"
	canaryAzure  = "CANARY-AZURE-1d8d"
)

// TestRuntime_ReloadFromBootstrapDocument builds a runtime from a bootstrap document and reloads it: the kept
// exporter stays, the changed one is rebuilt, a document with a broken entry changes nothing.
func TestRuntime_ReloadFromBootstrapDocument(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	doc := func(extra string) string {
		return fmt.Sprintf(
			"exporters:\n  files: {kind: file, outputDir: %s, flushInterval: 1h}\n  logs: {kind: log%s}\n",
			dir,
			extra,
		)
	}

	rec, _ := newLogRecorder()
	runtime := events.NewRuntime(
		&events.RuntimeConfig{Logger: slog.New(rec), Builder: events.Builder{Secrets: bootstrap.LoadOptions{}}},
	)

	changes := make(chan flagruntime.Change)
	require.NoError(t, runtime.Start(context.Background(), changes))
	require.ErrorIs(t, runtime.Start(context.Background(), changes), events.ErrAlreadyStarted)

	cfg, _ := parse(t, doc(""))

	report, err := runtime.Reload(context.Background(), events.SpecsFrom(cfg))
	require.NoError(t, err)
	assert.Equal(t, events.ReloadSet{Added: []string{nameFiles, "logs"}}, report.Exporters)

	cfg, _ = parse(t, doc(", logFormat: '{{.Key}}'"))

	report, err = runtime.Reload(context.Background(), events.SpecsFrom(cfg))
	require.NoError(t, err)
	assert.Equal(t, events.ReloadSet{Kept: []string{nameFiles}, Replaced: []string{"logs"}}, report.Exporters)

	// a broken document changes nothing
	broken := events.SpecsFrom(cfg)
	broken.Exporters = map[string]bootstrap.Exporter{nameFiles: {Kind: bootstrap.ExporterFile}}

	_, err = runtime.Reload(context.Background(), broken)
	require.ErrorIs(t, err, events.ErrInvalidSettings)
	assert.Len(t, runtime.Status().Exporters, 2, "one entry per name: files and logs")

	assert.True(t, runtime.Collect(ev(1)))

	shutdown, err := runtime.Shutdown(bounded(t))
	require.NoError(t, err)
	assert.Len(t, shutdown.Drained, 2)
	assert.False(t, runtime.Collect(ev(2)))
}

func TestRuntime_ReloadErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		specs   events.Specs
		wantErr error
		name    string
	}{
		{
			name:    "exporter",
			specs:   events.Specs{Exporters: map[string]bootstrap.Exporter{"e": {Kind: kindNope}}},
			wantErr: events.ErrUnknownKind,
		},
		{
			name:    "notifier",
			specs:   events.Specs{Notifiers: map[string]bootstrap.Notifier{"n": {Kind: kindNope}}},
			wantErr: events.ErrUnknownKind,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			runtime := events.NewRuntime(&events.RuntimeConfig{})
			require.NoError(t, runtime.Start(context.Background(), nil))

			t.Cleanup(func() { _, _ = runtime.Shutdown(canceled()) })

			_, err := runtime.Reload(context.Background(), tt.specs)
			require.ErrorIs(t, err, tt.wantErr)
		})
	}

	t.Run("after the shutdown", func(t *testing.T) {
		t.Parallel()

		runtime := events.NewRuntime(&events.RuntimeConfig{})
		_, err := runtime.Shutdown(bounded(t))
		require.NoError(t, err)

		_, err = runtime.Reload(context.Background(), events.Specs{})
		require.ErrorIs(t, err, events.ErrClosed)
	})
}

func TestRuntime_ErrorsNeverCarrySecrets(t *testing.T) {
	t.Parallel()

	env := map[string]string{envURL: "https://hook.example/" + canaryURL, envSecret: canarySecret}
	cfg, opts := parseDoc(
		t,
		connections+"exporters:\n  bad: {kind: webhook, endpointUrl: {fromEnv: URL}, secret: {fromEnv: SECRET},"+
			" headers: {X: {fromEnv: UNSET}}}\n",
		env,
	)

	_, err := events.Builder{Secrets: opts}.Exporters(context.Background(), events.SpecsFrom(cfg))
	require.ErrorIs(t, err, events.ErrUnresolvedSecret)
	assert.NotContains(t, err.Error(), canaryURL)
	assert.NotContains(t, err.Error(), canarySecret)
	assert.Contains(t, err.Error(), fmt.Sprintf("exporters[0] %q", nameBad))
}

// TestExporterStatus checks the counters of the status against an exporter that succeeds and one that fails.
func TestExporterStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		err         error
		name        string
		wantClass   events.ErrorClass
		wantState   string
		wantFail    uint64
		wantExport  uint64
		wantDropped uint64
	}{
		{name: "healthy", wantClass: events.ErrorNone, wantState: events.StateStopped, wantExport: 3},
		{
			name:        "failing",
			err:         context.DeadlineExceeded,
			wantClass:   events.ErrorTimeout,
			wantState:   events.StateStopped,
			wantFail:    1,
			wantDropped: 3,
		},
		{
			name:        "canceled",
			err:         context.Canceled,
			wantClass:   events.ErrorCanceled,
			wantState:   events.StateStopped,
			wantFail:    1,
			wantDropped: 3,
		},
		{
			name:        "other",
			err:         assert.AnError,
			wantClass:   events.ErrorOther,
			wantState:   events.StateStopped,
			wantFail:    1,
			wantDropped: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sink := &nopExporter{err: tt.err}
			cfg := exporter.Config{FlushInterval: 0, MaxEventInMemory: 1000}

			disp := events.NewDispatcher(&events.DispatcherConfig{})
			require.NoError(t, disp.Start(context.Background()))

			_, err := disp.Apply(
				context.Background(),
				[]events.ExporterDef{events.NewExporterDef("a", "file", "v1", sink, cfg)},
			)
			require.NoError(t, err)

			for i := range 3 {
				require.True(t, disp.Collect(ev(i)))
			}

			report, err := disp.Shutdown(bounded(t))
			require.NoError(t, err)

			got := statusOf(t, new(disp.Status()), "a")
			assert.Equal(t, tt.wantClass, got.LastError)
			assert.Equal(t, tt.wantState, got.State)
			assert.Equal(t, tt.wantExport, got.Exported)
			assert.Equal(t, tt.wantFail, got.FlushFailures)
			assert.Equal(t, tt.wantDropped, got.Dropped.Drain, "events a failed flush could not deliver")
			assert.Equal(t, int64(tt.wantDropped), report.Undelivered)
			assert.Equal(t, uint64(3), got.Handed)
			assert.Equal(t, uint64(1), got.Flushes)
		})
	}
}

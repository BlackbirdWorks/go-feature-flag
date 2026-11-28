package events_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/events"
)

// statusFunc is a status reader of a fixed status.
type statusFunc func() events.Status

func (f statusFunc) Status() events.Status { return f() }

// TestNewCollector_PipelineReasons: the drops of the pipeline have their own reason on the metric of the drops of an
// exporter, with the labels of the other reasons and none per flag set.
func TestNewCollector_PipelineReasons(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		reason    string
		drops     events.Drops
		wantValue int
	}{
		{name: "block timeout", drops: events.Drops{BlockTimeout: 7}, reason: events.ReasonBlockTimeout, wantValue: 7},
		{
			name: "retry exhausted", drops: events.Drops{RetryExhausted: 9}, reason: events.ReasonRetryExhausted,
			wantValue: 9,
		},
		{name: "overflow", drops: events.Drops{Overflow: 3}, reason: events.ReasonOverflow, wantValue: 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			source := statusFunc(func() events.Status {
				return events.Status{Exporters: []events.ExporterStatus{{Name: "a", Kind: kindFile, Dropped: tt.drops}}}
			})

			got := gather(t, source)["semaphore_exporter_dropped_events_total"]
			want := map[string]string{labelExporter: "a", labelKind: kindFile, "reason": tt.reason}

			assert.Equal(t, tt.wantValue, find(t, got, want).value)

			for _, sample := range got {
				assert.Len(t, sample.labels, 3, "exporter, kind and reason only")
			}
		})
	}
}

// TestDrops_Total: the new reasons count in the total of the drops.
func TestDrops_Total(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		drops events.Drops
		want  uint64
	}{
		{name: "none", want: 0},
		{name: "every reason", drops: events.Drops{
			QueueFull: 1, Shutdown: 2, NoExporter: 3, Overflow: 4, Drain: 5, NotPublic: 6, BlockTimeout: 7,
			RetryExhausted: 8,
		}, want: 36},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, tt.drops.Total())
		})
	}
}

// TestPipelineOf: the pipeline of an exporter is read from its entry of the bootstrap file, with the defaults the
// loader applies, and a built exporter carries it with its byte limit.
func TestPipelineOf(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		settings string
		want     events.Pipeline
		// bare reads an entry the loader has not completed.
		bare bool
	}{
		{
			name: "defaults", settings: "",
			want: events.Pipeline{
				Overflow: bootstrap.OverflowDropNewest, BlockTimeout: bootstrap.DefaultBlockTimeout,
				DrainTimeout: bootstrap.DefaultExporterDrainTimeout,
				Retry: events.RetryPolicy{
					MaxAttempts: bootstrap.DefaultRetryAttempts, InitialBackoff: bootstrap.DefaultRetryInitialBackoff,
					MaxBackoff: bootstrap.DefaultRetryMaxBackoff, MaxElapsed: bootstrap.DefaultRetryMaxElapsed,
				},
			},
		},
		{
			name: "every setting",
			settings: ", overflowPolicy: dropOldest, blockTimeout: 2s, drainTimeout: 3s, maxBytes: 1KiB, retry: " +
				"{maxAttempts: 2, initialBackoff: 5ms, maxBackoff: 6ms, maxElapsed: 7ms, onExhausted: drop}",
			want: events.Pipeline{
				Overflow: bootstrap.OverflowDropOldest, BlockTimeout: 2 * time.Second, DrainTimeout: 3 * time.Second,
				MaxBytes: 1024,
				Retry: events.RetryPolicy{
					MaxAttempts: 2, InitialBackoff: 5 * time.Millisecond, MaxBackoff: 6 * time.Millisecond,
					MaxElapsed: 7 * time.Millisecond, Drop: true,
				},
			},
		},
		{name: "no defaults", bare: true, want: events.Pipeline{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.bare {
				assert.Equal(t, events.Pipeline{}, events.PipelineOf(&bootstrap.Exporter{}))

				return
			}

			cfg, opts := parseDoc(t, "version: 2\nexporters:\n  e: {kind: log"+tt.settings+"}\n", nil)
			exp := cfg.Exporters["e"]

			assert.Equal(t, tt.want, events.PipelineOf(&exp))

			defs, err := events.Builder{Secrets: opts}.Exporters(context.Background(), events.SpecsFrom(cfg))
			require.NoError(t, err)
			assert.Equal(t, tt.want, defs[0].Pipeline)
			assert.Equal(t, tt.want.MaxBytes, defs[0].Config.MaxBytes)
		})
	}
}

// TestDispatcher_BlockedSenderLeaves: an exporter that is closed while its events wait for room in its queue wakes
// the wait at once; the events it held are counted by the drain, and no worker waited for them.
func TestDispatcher_BlockedSenderLeaves(t *testing.T) {
	t.Parallel()

	fac := newFactory()
	fac.gate = make(chan struct{})
	fac.entered = make(chan struct{}, 1)
	tim := newTimers(false)

	disp := startDispatcher(t, fac, &events.DispatcherConfig{
		Workers: 1, BatchSize: 1, HandoffSize: 2, After: tim.after,
	})

	expDef := def("a", "v1")
	expDef.Pipeline = events.Pipeline{
		Overflow: bootstrap.OverflowBlock, BlockTimeout: time.Hour, DrainTimeout: time.Nanosecond,
	}

	_, err := disp.Apply(context.Background(), []events.ExporterDef{expDef})
	require.NoError(t, err)

	require.True(t, disp.Collect(ev(0)))
	await(t, fac.entered, "the manager to hold the first event")

	for i := 1; i < 6; i++ {
		require.True(t, disp.Collect(ev(i)))
	}

	await(t, tim.armed, "the events to wait for room")
	require.Eventually(t, func() bool {
		return statusOf(t, new(disp.Status()), "a").Handed == 6
	}, wait, time.Millisecond, "the worker handed every event over without waiting")

	set, err := disp.Apply(context.Background(), nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"a"}, set.Removed)

	close(fac.gate)
	require.NoError(t, disp.WaitDrains(bounded(t)))

	got := statusOf(t, new(disp.Status()), "a")
	// the batch being placed when the exporter closed may still find room as the gate opens: it is exported or drained
	assert.Equal(t, got.Handed, got.Exported+got.Dropped.Drain, "every event is exported or drained")
	assert.GreaterOrEqual(t, got.Dropped.Drain, uint64(2), "the batches that waited behind it are drained")
	assert.Zero(t, got.Dropped.BlockTimeout)
	assert.Zero(t, disp.Status().Queue.Dropped.NoExporter)
}

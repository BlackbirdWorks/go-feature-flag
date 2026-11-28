package events_test

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/events"
	"github.com/blackbirdworks/semaphore/exporter"
)

// pipelineYAML is the exporters block of a relay that writes its events to a directory: the batches of the file
// exporter are the batches of the pipeline.
func pipelineYAML(dir, settings string) string {
	return "version: 2\nexporters:\n  out: {kind: file, outputDir: " + dir + ", format: json," +
		" filename: \"batch-{{ .Timestamp }}.{{ .Format }}\", flushInterval: 1h, " + settings + "}\n"
}

// written returns the sizes of the files of dir and the number of events (lines) they hold.
func written(tb testing.TB, dir string) ([]int64, int) {
	tb.Helper()

	files, err := filepath.Glob(filepath.Join(dir, "batch-*.json"))
	require.NoError(tb, err)

	sizes := make([]int64, 0, len(files))
	lines := 0

	for _, name := range files {
		raw, readErr := os.ReadFile(name)
		require.NoError(tb, readErr)

		sizes = append(sizes, int64(len(raw)))

		scan := bufio.NewScanner(bytes.NewReader(raw))
		for scan.Scan() {
			lines++
		}
	}

	return sizes, lines
}

// TestRuntime_PipelineFromYAML runs the event pipeline of a relay from its bootstrap document with a file exporter:
// the batches respect maxBytes, a failing exporter is retried and then counted, a reload that removes the exporter
// drains it, and a shutdown drains within the deadline.
func TestRuntime_PipelineFromYAML(t *testing.T) {
	t.Parallel()

	const count = 12

	size := int64(exporter.EventSize(ev(0)))

	tests := []struct {
		name         string
		settings     string
		broken       bool
		reload       bool
		wantEvents   int
		wantMaxFile  int64
		wantMinFiles int
		wantCalls    uint64
		wantDropped  uint64
		wantRetried  uint64
	}{
		{
			name: "batches respect maxBytes", wantEvents: count, wantMaxFile: 3 * size, wantMinFiles: 4,
			settings: "maxBytes: \"" + sizeText(3*size) + "\"",
		},
		{
			name: "reload that removes the exporter drains it", reload: true, wantEvents: count,
			wantMaxFile: 1 << 20, wantMinFiles: 1, settings: "overflowPolicy: block",
		},
		{
			name: "shutdown drains within the deadline", wantEvents: count, wantMaxFile: 1 << 20, wantMinFiles: 1,
			settings: "drainTimeout: 1m",
		},
		{
			name: "failing exporter is retried then counted", broken: true, wantCalls: 3, wantDropped: count,
			settings: "retry: {maxAttempts: 3, initialBackoff: 1s, maxBackoff: 1s, maxElapsed: 1m}", wantRetried: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()

			out := dir
			if tt.broken {
				// a file where the directory should be: every write fails
				out = filepath.Join(dir, "blocked")
				require.NoError(t, os.WriteFile(out, []byte("x"), 0o600))
			}

			cfg, opts := parseDoc(t, pipelineYAML(out, tt.settings), nil)

			tim := newTimers(true)
			relay := newRuntime(t, &events.RuntimeConfig{
				Builder: events.Builder{Secrets: opts}, After: tim.after, Rand: halfRand,
			})
			require.NoError(t, relay.Start(context.Background(), nil))

			_, err := relay.Reload(context.Background(), events.SpecsFrom(cfg))
			require.NoError(t, err)

			for i := range count {
				require.True(t, relay.Collect(ev(i)))
			}

			// the events are in the exporter (a flush interval of an hour holds them) before the drain starts
			require.Eventually(t, func() bool {
				return statusOf(t, new(relay.Status()), "out").Handed == count
			}, wait, time.Millisecond)

			if tt.reload {
				set, reloadErr := relay.Reload(context.Background(), events.Specs{})
				require.NoError(t, reloadErr)
				assert.Equal(t, []string{"out"}, set.Exporters.Removed)
				assert.Empty(t, set.Exporters.Cut)
			}

			report, err := relay.Shutdown(bounded(t))
			require.NoError(t, err)

			sizes, got := written(t, dir)
			status := statusOf(t, new(relay.Status()), "out")

			assert.Equal(t, tt.wantEvents, got)
			assert.GreaterOrEqual(t, len(sizes), tt.wantMinFiles)

			for _, fileSize := range sizes {
				assert.LessOrEqual(t, fileSize, tt.wantMaxFile)
			}

			assert.Equal(t, uint64(tt.wantEvents), status.Exported)
			assert.Equal(t, tt.wantDropped, status.Dropped.RetryExhausted)
			assert.Equal(t, tt.wantCalls, status.FlushFailures)
			assert.Len(t, tim.recorded(), int(tt.wantRetried))
			assert.Zero(t, report.Undelivered)

			for _, drained := range report.Drained {
				assert.False(t, drained.Cut)
			}
		})
	}
}

// sizeText writes a number of bytes as a size of the bootstrap file.
func sizeText(size int64) string { return strconv.FormatInt(size, 10) + "B" }

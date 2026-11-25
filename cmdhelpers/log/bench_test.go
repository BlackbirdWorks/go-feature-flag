package log_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmdhelpers/log"
)

// sink is a writer that drops what it gets. The handlers of the standard library are measured through it: the
// logger of the standard library has no handler made to discard the output of another handler.
type sink struct{}

func (sink) Write(p []byte) (int, error) { return len(p), nil }

// logFiveAttrs is the record of the benchmarks: 5 typed attributes, like a request log line.
func logFiveAttrs(ctx context.Context, l *slog.Logger, level slog.Level) {
	l.LogAttrs(ctx, level, request,
		slog.String("remote_ip", "10.0.0.1"),
		slog.String("host", "localhost:1031"),
		slog.Int("status", 200),
		slog.Int64("size", 1234),
		slog.Duration("latency", 1500*time.Microsecond),
	)
}

func newBenchLogger(tb testing.TB, format string) *slog.Logger {
	tb.Helper()

	l, err := log.New(format, info, log.WithWriter(sink{}))
	require.NoError(tb, err)

	return l.Slog
}

func BenchmarkLogger(b *testing.B) {
	ctx := context.Background()

	tests := []struct {
		logger *slog.Logger
		name   string
		level  slog.Level
	}{
		{name: "json/enabled/5attrs", logger: newBenchLogger(b, json2), level: slog.LevelInfo},
		{name: "logfmt/enabled/5attrs", logger: newBenchLogger(b, logfmt), level: slog.LevelInfo},
		{name: "json/disabled/5attrs", logger: newBenchLogger(b, json2), level: slog.LevelDebug},
		{name: "stdlib-json/enabled/5attrs", logger: slog.New(slog.NewJSONHandler(sink{}, nil)),
			level: slog.LevelInfo},
		{name: "stdlib-text/enabled/5attrs", logger: slog.New(slog.NewTextHandler(sink{}, nil)),
			level: slog.LevelInfo},
		{name: "stdlib-json-same-keys/enabled/5attrs", logger: slog.New(slog.NewJSONHandler(sink{},
			&slog.HandlerOptions{ReplaceAttr: stdlibKeys})), level: slog.LevelInfo},
	}
	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				logFiveAttrs(ctx, tt.logger, tt.level)
			}
		})
	}
}

// stdlibKeys is the least a stdlib handler needs to get close to the output of the handler: the key and the
// value of the time and of the level. It still can not order the keys, add the caller or write durations in seconds.
func stdlibKeys(_ []string, attr slog.Attr) slog.Attr {
	switch attr.Key {
	case slog.TimeKey:
		return slog.Float64(log.TimeKey, float64(attr.Value.Time().UnixNano())/1e9)
	case slog.LevelKey:
		return slog.String(log.LevelKey, info)
	}

	return attr
}

// TestAllocations holds the allocation budget of the logging path: an enabled record with 5 typed attributes and a
// disabled one allocate nothing. It is not parallel on purpose: the allocations of the other tests would count.
//
//nolint:paralleltest // AllocsPerRun counts the allocations of the parallel tests too
func TestAllocations(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name       string
		format     string
		level      slog.Level
		wantAllocs float64
	}{
		{name: "json enabled", format: json2, level: slog.LevelInfo},
		{name: "logfmt enabled", format: logfmt, level: slog.LevelInfo},
		{name: "json disabled", format: json2, level: slog.LevelDebug},
		{name: "logfmt disabled", format: logfmt, level: slog.LevelDebug},
	}
	//nolint:paralleltest // subtests call AllocsPerRun, which counts allocations from concurrent tests
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := newBenchLogger(t, tt.format)

			got := testing.AllocsPerRun(100, func() { logFiveAttrs(ctx, logger, tt.level) })

			assert.InDelta(t, tt.wantAllocs, got, 0)
		})
	}
}

package manifest

import (
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/cli/helper"
)

func TestSlogLevelToOutputLevel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		want  helper.Level
		level slog.Level
	}{
		{name: "error maps to error", level: slog.LevelError, want: helper.ErrorLevel},
		{name: "warn maps to warn", level: slog.LevelWarn, want: helper.WarnLevel},
		{name: "info maps to info", level: slog.LevelInfo, want: helper.InfoLevel},
		{name: "debug maps to default", level: slog.LevelDebug, want: helper.DefaultLevel},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, slogLevelToOutputLevel(tt.level))
		})
	}
}

func TestCaptureHandler(t *testing.T) {
	t.Parallel()

	var records []capturedRecord

	h := slogHandlerForRecords(&records).WithGroup("g").WithAttrs([]slog.Attr{slog.String("k", "v")})
	require.True(t, h.Enabled(t.Context(), slog.LevelDebug))

	rec := slog.NewRecord(time.Time{}, slog.LevelInfo, "hello", 0)
	rec.AddAttrs(slog.String("a", "b"))
	require.NoError(t, h.Handle(t.Context(), rec))

	require.Len(t, records, 1)
	assert.Equal(t, slog.LevelInfo, records[0].level)
	assert.Equal(t, "hello a=b", records[0].message)
}

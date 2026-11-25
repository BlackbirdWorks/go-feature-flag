package slogutil

import (
	"context"
	"fmt"
	"log/slog"
	"os"
)

// PlainHandler is a slog handler that writes each record as its message followed by its attributes (key=value),
// without the time and the level.
type PlainHandler struct {
	Writer *os.File
}

// Handle implements the [slog.Handler] interface.
func (h *PlainHandler) Handle(_ context.Context, r slog.Record) error {
	line := r.Message
	r.Attrs(func(a slog.Attr) bool {
		line += " " + a.Key + "=" + a.Value.String()

		return true
	})

	_, err := h.Writer.WriteString(line + "\n")
	if err != nil {
		return fmt.Errorf("write log message: %w", err)
	}

	return nil
}

// Enabled implements the [slog.Handler] interface.
func (*PlainHandler) Enabled(_ context.Context, _ slog.Level) bool {
	return true
}

// WithAttrs implements the [slog.Handler] interface.
func (h *PlainHandler) WithAttrs(_ []slog.Attr) slog.Handler {
	return h
}

// WithGroup implements the [slog.Handler] interface.
func (h *PlainHandler) WithGroup(_ string) slog.Handler {
	return h
}

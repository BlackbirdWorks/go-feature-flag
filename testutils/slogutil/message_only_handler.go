package slogutil

import (
	"context"
	"fmt"
	"log/slog"
	"os"
)

// MessageOnlyHandler is a slog handler that writes only the message of each record.
type MessageOnlyHandler struct {
	Writer *os.File
}

// Handle implements the [slog.Handler] interface.
func (h *MessageOnlyHandler) Handle(_ context.Context, r slog.Record) error {
	_, err := h.Writer.WriteString(r.Message + "\n")
	if err != nil {
		return fmt.Errorf("write log message: %w", err)
	}

	return nil
}

// Enabled implements the [slog.Handler] interface.
func (_ *MessageOnlyHandler) Enabled(_ context.Context, _ slog.Level) bool {
	return true
}

// WithAttrs implements the [slog.Handler] interface.
func (h *MessageOnlyHandler) WithAttrs(_ []slog.Attr) slog.Handler {
	return h
}

// WithGroup implements the [slog.Handler] interface.
func (h *MessageOnlyHandler) WithGroup(_ string) slog.Handler {
	return h
}

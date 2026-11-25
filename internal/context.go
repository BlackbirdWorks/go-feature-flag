package internal

import (
	"context"
	"log/slog"
)

// ContextOrBackground returns ctx, or the background context when ctx is nil. Passing a nil context is a mistake
// of the caller (staticcheck SA1012), the entry points of the library tolerate it rather than panic.
func ContextOrBackground(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}

	return ctx
}

// LoggerOrDiscard returns logger, or a logger that discards everything when logger is nil: the entry points that
// take a logger give their components a usable one, so that none of them needs to check for nil.
func LoggerOrDiscard(logger *slog.Logger) *slog.Logger {
	if logger == nil {
		return slog.New(slog.DiscardHandler)
	}

	return logger
}

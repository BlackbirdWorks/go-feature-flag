package fflog

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"strings"
	"time"

	"github.com/blackbirdworks/semaphore/modules/core/clock"
)

// LogDateFormat is the default log format.
const LogDateFormat = time.RFC3339

// FFLogger is the internal logger struct for Semaphore.
type FFLogger struct {
	LeveledLogger *slog.Logger
	LegacyLogger  *log.Logger
}

// Error is the function to use to log error.
func (f *FFLogger) Error(msg string, keysAndValues ...any) {
	f.ErrorContext(context.Background(), msg, keysAndValues...)
}

// ErrorContext is the function to use to log error with a context.
//
//nolint:contextcheck // defensive fallback when a caller passes a nil context
func (f *FFLogger) ErrorContext(ctx context.Context, msg string, keysAndValues ...any) {
	if ctx == nil {
		ctx = context.Background()
	}
	if f != nil && f.LeveledLogger != nil {
		f.LeveledLogger.ErrorContext(ctx, msg, keysAndValues...)

		return
	}
	f.legacyLog("ERROR", msg, keysAndValues...)
}

// Info is the function to use to log info.
func (f *FFLogger) Info(msg string, keysAndValues ...any) {
	f.InfoContext(context.Background(), msg, keysAndValues...)
}

// InfoContext is the function to use to log info with a context.
//
//nolint:contextcheck // defensive fallback when a caller passes a nil context
func (f *FFLogger) InfoContext(ctx context.Context, msg string, keysAndValues ...any) {
	if ctx == nil {
		ctx = context.Background()
	}
	if f != nil && f.LeveledLogger != nil {
		f.LeveledLogger.InfoContext(ctx, msg, keysAndValues...)

		return
	}
	f.legacyLog("INFO", msg, keysAndValues...)
}

// Debug is the function to use to log debug.
func (f *FFLogger) Debug(msg string, keysAndValues ...any) {
	f.DebugContext(context.Background(), msg, keysAndValues...)
}

// DebugContext is the function to use to log debug with a context.
//
//nolint:contextcheck // defensive fallback when a caller passes a nil context
func (f *FFLogger) DebugContext(ctx context.Context, msg string, keysAndValues ...any) {
	if ctx == nil {
		ctx = context.Background()
	}
	if f != nil && f.LeveledLogger != nil {
		f.LeveledLogger.DebugContext(ctx, msg, keysAndValues...)

		return
	}
	f.legacyLog("DEBUG", msg, keysAndValues...)
}

// Warn is the function to use to log warn.
func (f *FFLogger) Warn(msg string, keysAndValues ...any) {
	f.WarnContext(context.Background(), msg, keysAndValues...)
}

// WarnContext is the function to use to log warn with a context.
//
//nolint:contextcheck // defensive fallback when a caller passes a nil context
func (f *FFLogger) WarnContext(ctx context.Context, msg string, keysAndValues ...any) {
	if ctx == nil {
		ctx = context.Background()
	}
	if f != nil && f.LeveledLogger != nil {
		f.LeveledLogger.WarnContext(ctx, msg, keysAndValues...)

		return
	}
	f.legacyLog("WARN", msg, keysAndValues...)
}

// GetLogLogger is returning a classic logger from a slog logger.
func (f *FFLogger) GetLogLogger(level slog.Level) *log.Logger {
	if f.LeveledLogger != nil {
		return slog.NewLogLogger(f.LeveledLogger.Handler(), level)
	}

	return f.LegacyLogger
}

func (f *FFLogger) legacyLog(level, msg string, keysAndValues ...any) {
	if f != nil && f.LegacyLogger != nil {
		if len(keysAndValues) == 0 {
			f.LegacyLogger.Printf("%s %s %s", clock.Now().Format("2006/01/02 15:04:05"), level, msg)

			return
		}

		attrs := make([]string, 0)
		for _, attr := range keysAndValues {
			attrs = append(attrs, fmt.Sprintf("%v", attr))
		}
		f.LegacyLogger.Printf(
			"%s %s %s %v",
			clock.Now().Format("2006/01/02 15:04:05"),
			level,
			msg,
			strings.Join(attrs, " "),
		)
	}
}

// ConvertToFFLogger is converting a classic logger to our internal logger.
func ConvertToFFLogger(logger *log.Logger) *FFLogger {
	return &FFLogger{
		LegacyLogger: logger,
	}
}

package log

import (
	"errors"
	"fmt"
	"log/slog"
)

// LevelFatal is the level of the messages logged right before the process exits.
const LevelFatal = slog.Level(12)

// ErrUnrecognizedLevel is returned for a log level that is not one of the supported names.
var ErrUnrecognizedLevel = errors.New("unrecognized level")

// ParseLevel converts a configured log level to a [slog.Level]. The accepted names are the ones of
// the former zap based logger: debug, info (also empty), warn, error, dpanic, panic and fatal, in lower or
// upper case.
func ParseLevel(name string) (slog.Level, error) {
	switch name {
	case "debug", "DEBUG":
		return slog.LevelDebug, nil
	case "info", "INFO", "":
		return slog.LevelInfo, nil
	case "warn", "WARN":
		return slog.LevelWarn, nil
	case "error", "ERROR":
		return slog.LevelError, nil
	case "dpanic", "DPANIC", "panic", "PANIC", "fatal", "FATAL":
		return LevelFatal, nil
	default:
		return 0, fmt.Errorf("%w: %q", ErrUnrecognizedLevel, name)
	}
}

// levelName is the level string of the output.
func levelName(l slog.Level) string {
	switch {
	case l < slog.LevelInfo:
		return "debug"
	case l < slog.LevelWarn:
		return "info"
	case l < slog.LevelError:
		return "warn"
	case l < LevelFatal:
		return "error"
	default:
		return "fatal"
	}
}

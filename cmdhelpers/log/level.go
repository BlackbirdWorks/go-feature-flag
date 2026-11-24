package log

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
)

// LevelFatal is the level of the messages logged right before the process exits.
const LevelFatal = slog.Level(12)

// ErrUnrecognizedLevel is returned for a log level that is not one of the supported names.
var ErrUnrecognizedLevel = errors.New("unrecognized level")

// ParseLevel converts a configured log level. It accepts what zap accepted (zapcore.Level.UnmarshalText): the
// names debug, info (also empty), warn (also warning), error, dpanic, panic and fatal, in any letter case.
// dpanic, panic and fatal are above the error level, as in zap: only the records of the fatal level pass.
func ParseLevel(name string) (slog.Level, error) {
	switch strings.ToLower(name) {
	case "debug":
		return slog.LevelDebug, nil
	case "info", "":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	case "dpanic", "panic", "fatal":
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

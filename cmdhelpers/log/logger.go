// Package log builds the logger of the relay proxy: a [slog.Logger] writing JSON or logfmt lines to a writer
// (stderr by default), with the level and the format of the configuration.
package log

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

// ErrUnknownFormat is returned for a log format that is neither json nor logfmt.
var ErrUnknownFormat = errors.New("unknown log format")

// Logger is the logger of the process, built from the log level and the log format. The handlers of every logger
// derived from [Logger.Slog] (With, WithGroup) follow [Logger.Update].
type Logger struct {
	// Slog is the logger to pass to the code that logs.
	Slog *slog.Logger
	core *core
}

// Option configures the logger.
type Option func(*options)

type options struct {
	out io.Writer
}

// WithWriter sets where the logger writes. The default is stderr. Writes are serialised by the logger,
// the writer does not need to be safe for concurrent use.
func WithWriter(w io.Writer) Option {
	return func(o *options) { o.out = w }
}

// NewDefault builds the logger that is used until the configuration is read: json lines at the info level on
// stderr.
func NewDefault(opts ...Option) *Logger {
	o := options{out: os.Stderr}
	for _, opt := range opts {
		opt(&o)
	}
	c := newCore(o.out)

	return &Logger{core: c, Slog: slog.New(&handler{core: c})}
}

// New builds a logger. An empty format is json, an empty level is info.
func New(format, level string, opts ...Option) (*Logger, error) {
	logger := NewDefault(opts...)
	err := logger.Update(format, level)
	if err != nil {
		return nil, err
	}

	return logger, nil
}

// ParseFormat converts a configured log format.
func ParseFormat(name string) (Format, error) {
	switch strings.ToLower(name) {
	case "json", "":
		return FormatJSON, nil
	case "logfmt":
		return FormatLogfmt, nil
	default:
		return 0, fmt.Errorf("%w: %q", ErrUnknownFormat, name)
	}
}

// Update changes the format and the level of the logger, and of every logger derived from it.
// It is safe for concurrent use. When it fails nothing changes.
func (l *Logger) Update(format, level string) error {
	parsedFormat, err := ParseFormat(format)
	if err != nil {
		return err
	}
	lvl, err := ParseLevel(level)
	if err != nil {
		return err
	}
	l.core.format.Store(int32(parsedFormat))
	l.core.level.Set(lvl)

	return nil
}

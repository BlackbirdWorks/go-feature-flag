package fflog

import "log/slog"

// FFLogger is the logger of Semaphore: the standard [slog.Logger].
//
// Deprecated: use [slog.Logger]. This alias only exists while the callers move to the standard type, and the
// package goes away with them.
type FFLogger = slog.Logger

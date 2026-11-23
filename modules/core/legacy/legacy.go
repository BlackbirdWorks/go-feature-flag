// Package legacy holds what the Semaphore rename needs to keep the names of GO Feature Flag
// working: the canonical Semaphore name is always read first and the legacy name is accepted as
// an alias. Using a legacy name logs one warning per process and per kind of name, never one per
// request.
package legacy

import (
	"log/slog"
	"sync"
	"sync/atomic"
)

// LogFunc receives the warning for one kind of legacy name.
type LogFunc func(kind, message string)

// Warner logs one warning per kind of legacy name.
type Warner struct {
	seen sync.Map
	logf atomic.Pointer[LogFunc]
}

// NewWarner creates a Warner that sends its warnings to logf (slog.Default when nil).
func NewWarner(logf LogFunc) *Warner {
	w := &Warner{}
	w.SetLogFunc(logf)
	return w
}

// SetLogFunc replaces the destination of the warnings (slog.Default when logf is nil).
func (w *Warner) SetLogFunc(logf LogFunc) {
	if logf == nil {
		logf = func(kind, message string) {
			slog.Warn(message, "legacyName", kind)
		}
	}
	w.logf.Store(&logf)
}

// Warn logs message the first time it is called for kind, and does nothing afterwards.
func (w *Warner) Warn(kind, message string) {
	if _, loaded := w.seen.LoadOrStore(kind, struct{}{}); loaded {
		return
	}
	(*w.logf.Load())(kind, message)
}

// Default is the Warner used by Semaphore itself.
var Default = NewWarner(nil)

// Kinds of legacy names, one warning is logged per kind.
const (
	KindConfigFile        = "config-file"
	KindFlagFile          = "flag-file"
	KindEnvPrefix         = "env-prefix"
	KindEvaluationContext = "evaluation-context-key"
)

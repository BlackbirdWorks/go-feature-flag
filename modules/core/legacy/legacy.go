// Package legacy holds what the Semaphore rename needs to keep the names of GO Feature Flag
// working: the canonical Semaphore name is always read first and the legacy name is accepted as
// an alias. Using a legacy name logs one warning per Warner and per kind of name, never one per
// request.
package legacy

import "sync"

// LogFunc receives the warning for one kind of legacy name.
type LogFunc func(kind, message string)

// Warner logs one warning per kind of legacy name. Whoever reads names that may be legacy owns a
// Warner (the relay's configuration loader, the evaluation Service of a client, a CLI command), so
// that two of them never share the "already warned" state.
type Warner struct {
	logf LogFunc
	seen sync.Map
}

// NewWarner creates a Warner that sends its warnings to logf (they are dropped when logf is nil).
func NewWarner(logf LogFunc) *Warner {
	if logf == nil {
		logf = func(string, string) {}
	}

	return &Warner{logf: logf}
}

// Warn logs message the first time it is called for kind, and does nothing afterwards. A nil Warner
// does nothing.
func (w *Warner) Warn(kind, message string) {
	if w == nil {
		return
	}

	if _, loaded := w.seen.LoadOrStore(kind, struct{}{}); loaded {
		return
	}

	w.logf(kind, message)
}

// Kinds of legacy names, one warning is logged per kind.
const (
	KindConfigFile        = "config-file"
	KindFlagFile          = "flag-file"
	KindEnvPrefix         = "env-prefix"
	KindEvaluationContext = "evaluation-context-key"
)

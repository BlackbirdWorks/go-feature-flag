package slogutil

import (
	"context"
	"log/slog"
	"strings"
	"sync"
)

// Entry is a record captured by an observer.
type Entry struct {
	Message string
	Attrs   []slog.Attr
	Level   slog.Level
}

// ContextMap returns the attributes of the record by key. An error is its message.
func (e Entry) ContextMap() map[string]any {
	attrsMap := make(map[string]any, len(e.Attrs))
	for _, attr := range e.Attrs {
		v := attr.Value.Resolve().Any()
		if err, ok := v.(error); ok {
			v = err.Error()
		}

		attrsMap[attr.Key] = v
	}

	return attrsMap
}

// ObservedLogs are the records captured by an observer.
type ObservedLogs struct {
	mu      *sync.Mutex
	entries *[]Entry
	// filter selects the entries of a filtered view.
	filter func(Entry) bool
}

// NewObserver returns a logger that keeps the records of the level and above, and the way to read them.
func NewObserver(level slog.Level) (*slog.Logger, *ObservedLogs) {
	logs := &ObservedLogs{mu: &sync.Mutex{}, entries: &[]Entry{}}

	return slog.New(&observerHandler{logs: logs, level: level}), logs
}

// All returns the captured records.
func (o *ObservedLogs) All() []Entry {
	o.mu.Lock()
	defer o.mu.Unlock()

	out := make([]Entry, 0, len(*o.entries))
	for _, e := range *o.entries {
		if o.filter == nil || o.filter(e) {
			out = append(out, e)
		}
	}

	return out
}

// Len returns the number of captured records.
func (o *ObservedLogs) Len() int { return len(o.All()) }

// FilterMessage returns the records with exactly this message.
func (o *ObservedLogs) FilterMessage(msg string) *ObservedLogs {
	return o.filtered(func(e Entry) bool { return e.Message == msg })
}

// FilterMessageSnippet returns the records whose message contains the snippet.
func (o *ObservedLogs) FilterMessageSnippet(snippet string) *ObservedLogs {
	return o.filtered(func(e Entry) bool { return strings.Contains(e.Message, snippet) })
}

func (o *ObservedLogs) filtered(keep func(Entry) bool) *ObservedLogs {
	prev := o.filter

	return &ObservedLogs{mu: o.mu, entries: o.entries, filter: func(e Entry) bool {
		return (prev == nil || prev(e)) && keep(e)
	}}
}

type observerHandler struct {
	logs  *ObservedLogs
	attrs []slog.Attr
	level slog.Level
}

func (h *observerHandler) Enabled(_ context.Context, l slog.Level) bool { return l >= h.level }

//nolint:gocritic // hugeParam: signature fixed by log/slog.Handler
func (h *observerHandler) Handle(_ context.Context, r slog.Record) error {
	entry := Entry{Message: r.Message, Level: r.Level, Attrs: append([]slog.Attr(nil), h.attrs...)}
	r.Attrs(func(a slog.Attr) bool {
		// like the handlers, skip the empty attributes.
		if !a.Equal(slog.Attr{}) {
			entry.Attrs = append(entry.Attrs, a)
		}

		return true
	})
	h.logs.mu.Lock()
	*h.logs.entries = append(*h.logs.entries, entry)
	h.logs.mu.Unlock()

	return nil
}

func (h *observerHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &observerHandler{logs: h.logs, level: h.level, attrs: append(append([]slog.Attr(nil), h.attrs...), attrs...)}
}

func (h *observerHandler) WithGroup(string) slog.Handler { return h }

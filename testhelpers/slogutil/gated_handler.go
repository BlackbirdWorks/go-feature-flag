package slogutil

import (
	"context"
	"log/slog"
	"strings"
	"sync"
)

// GatedHandler wraps an [slog.Handler] and blocks records whose message contains match until Release is called.
type GatedHandler struct {
	inner   slog.Handler
	gate    chan struct{}
	entered chan struct{}
	match   string
	once    sync.Once
	relOnce sync.Once
}

// NewGatedHandler returns a GatedHandler that delegates to inner and blocks matching records until Release.
func NewGatedHandler(inner slog.Handler, match string) *GatedHandler {
	return &GatedHandler{
		inner:   inner,
		match:   match,
		gate:    make(chan struct{}),
		entered: make(chan struct{}),
	}
}

// Entered is closed when the first matching record enters Handle.
func (g *GatedHandler) Entered() <-chan struct{} { return g.entered }

// Release unblocks all gated Handle calls. It is safe to call multiple times.
func (g *GatedHandler) Release() {
	g.relOnce.Do(func() { close(g.gate) })
}

// Enabled implements [slog.Handler]. Always returns true so gated matching inspects every record
// even when inner discards.
func (*GatedHandler) Enabled(context.Context, slog.Level) bool {
	return true
}

// Handle implements [slog.Handler].
//
//nolint:gocritic // hugeParam: signature fixed by log/slog.Handler
func (g *GatedHandler) Handle(ctx context.Context, r slog.Record) error {
	if strings.Contains(r.Message, g.match) {
		g.once.Do(func() { close(g.entered) })
		<-g.gate
	}

	return g.inner.Handle(ctx, r) //nolint:wrapcheck // transparent test handler wrapper
}

// WithAttrs implements [slog.Handler].
func (g *GatedHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &GatedHandler{
		inner:   g.inner.WithAttrs(attrs),
		match:   g.match,
		gate:    g.gate,
		entered: g.entered,
	}
}

// WithGroup implements [slog.Handler].
func (g *GatedHandler) WithGroup(name string) slog.Handler {
	return &GatedHandler{
		inner:   g.inner.WithGroup(name),
		match:   g.match,
		gate:    g.gate,
		entered: g.entered,
	}
}

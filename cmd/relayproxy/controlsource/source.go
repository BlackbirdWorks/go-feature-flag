package controlsource

import (
	"context"
	"log/slog"
	"sync/atomic"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/internal/manifestreader"
)

// Reader is what the source needs from the manifest reader: *manifestreader.Reader implements it.
type Reader interface {
	// Snapshots is the latest-wins channel of snapshots; it is closed when the reader stops.
	Snapshots() <-chan *manifestreader.Snapshot
	// Status is the state of the reader at the end of its last round.
	Status() manifestreader.Status
}

// Engine is what the source needs from the reconcile engine: *flagruntime.Engine implements it.
type Engine interface {
	// Submit hands a content to the engine without blocking (latest wins).
	Submit(content *flagruntime.Content)
	// Result is the result of the last round of the engine.
	Result() flagruntime.RoundResult
}

// Status is the joined status of the reader, the source and the engine. It never holds an API key or the bytes of
// an object. Its fields are values or immutable trees, safe to hand to an encoder.
type Status struct {
	// Round is the result of the last round of the engine, applied, ignored or rejected.
	Round flagruntime.RoundResult `json:"round"`
	// Runtime is the status tree of the runtime that serves now.
	Runtime *flagruntime.Status `json:"runtime"`
	// Reader is the status of the manifest reader.
	Reader manifestreader.Status `json:"reader"`
	// Received counts the snapshots the source took from the reader and handed to the engine.
	Received uint64 `json:"received"`
	// LastGeneration is the generation of the last snapshot that was handed to the engine.
	LastGeneration uint64 `json:"lastGeneration"`
}

// Source runs the goroutine that feeds the engine from the reader.
type Source struct {
	reader   Reader
	engine   Engine
	holder   *flagruntime.Holder
	logger   *slog.Logger
	received atomic.Uint64
	lastGen  atomic.Uint64
}

// New returns a source. holder is the holder the engine stores into; a nil logger discards.
func New(reader Reader, engine Engine, holder *flagruntime.Holder, logger *slog.Logger) *Source {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}

	return &Source{reader: reader, engine: engine, holder: holder, logger: logger}
}

// Run consumes the snapshots of the reader until ctx is done or the reader closes its channel, and returns then.
// It starts no goroutine of its own: it is the one goroutine of the source, so nothing is left behind. The reader
// delivers latest-wins into a channel of one, and the engine's Submit is latest-wins too: a burst of snapshots
// reaches the engine as its newest content, never as a queue. Run must be called once.
func (s *Source) Run(ctx context.Context) {
	snaps := s.reader.Snapshots()

	for {
		select {
		case <-ctx.Done():
			return
		case snap, open := <-snaps:
			if !open {
				return
			}

			s.deliver(ctx, snap)
		}
	}
}

// Status joins the status of the reader, the engine and the running runtime. It reads atomics and immutable
// values only: it never blocks the reader, the source or the engine.
func (s *Source) Status() Status {
	return Status{
		Runtime:        s.holder.Load().Status,
		Reader:         s.reader.Status(),
		Round:          s.engine.Result(),
		Received:       s.received.Load(),
		LastGeneration: s.lastGen.Load(),
	}
}

func (s *Source) deliver(ctx context.Context, snap *manifestreader.Snapshot) {
	if snap == nil {
		return
	}

	s.received.Add(1)

	content := Content(snap)

	// recorded before the engine can answer: whoever reads the result of the round reads this generation too
	s.lastGen.Store(content.Generation)

	s.engine.Submit(content)

	if s.logger.Enabled(ctx, slog.LevelDebug) {
		s.logger.LogAttrs(ctx, slog.LevelDebug, "snapshot handed to the runtime",
			slog.Uint64("generation", content.Generation), slog.Int("objects", len(content.Objects)))
	}
}

// Content maps a snapshot to the content of the engine. The bytes of an object are not copied: the content shares
// them with the snapshot (both are immutable). The states are mapped one to one: ok, stale and missing; a state
// that this version does not know is reported as missing with the reason it came with, so the engine keeps the
// last good content of the object.
func Content(snap *manifestreader.Snapshot) *flagruntime.Content {
	objects := make(map[string]flagruntime.Object, len(snap.Objects))

	for name, obj := range snap.Objects {
		objects[name] = flagruntime.Object{
			Kind:     obj.Kind,
			Format:   obj.Format,
			Checksum: obj.Checksum.Value,
			State:    objectState(obj.State),
			Reason:   obj.Reason,
			Bytes:    obj.Bytes,
		}
	}

	return &flagruntime.Content{Objects: objects, Generation: snap.Generation}
}

func objectState(state manifestreader.State) flagruntime.ObjectState {
	switch state {
	case manifestreader.StateOK:
		return flagruntime.ObjectOK
	case manifestreader.StateStale:
		return flagruntime.ObjectStale
	case manifestreader.StateMissing:
		return flagruntime.ObjectMissing
	}

	return flagruntime.ObjectMissing
}

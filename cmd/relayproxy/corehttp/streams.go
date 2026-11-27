package corehttp

import (
	"context"
	"errors"
	"fmt"
	"runtime/pprof"
	"sync"
	"sync/atomic"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service/stream"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
	"github.com/blackbirdworks/semaphore/notifier"
)

// DefaultMaxPending is the number of flag sets whose change can wait for a stream service before the oldest one is
// dropped.
const DefaultMaxPending = 1024

// ErrNotStopped is returned by [Streams.Close] when a stream service did not finish its last delivery before the
// context ended. The worker ends, and closes its service, as soon as that delivery returns.
var ErrNotStopped = errors.New("corehttp: the stream workers did not stop")

// StreamsConfig configures [NewStreamsWith].
type StreamsConfig struct {
	// Websocket and SSE are the services of the streams.
	Websocket stream.WebsocketService
	SSE       stream.SSEService
	// Label is the value of the pprof label of the workers; it tells the goroutines of one Streams apart.
	Label string
	// MaxPending bounds the flag sets that wait for a service; 0 means [DefaultMaxPending].
	MaxPending int
}

// Streams are the SSE and websocket services of the streams of flag changes. A core calls [Streams.Broadcast] for
// every change (set it as the Broadcast of the dependencies of the core), and the server serves the connections.
// A change never waits for a client: each service has its own worker and queue, so a stalled websocket delays
// neither the core nor the SSE clients.
type Streams struct {
	ws   stream.WebsocketService
	sse  stream.SSEService
	lane [2]*lane
}

// NewStreams returns the streams on the stream package services, built with its options.
func NewStreams(opts ...stream.Option) *Streams {
	return NewStreamsWith(&StreamsConfig{
		Websocket: stream.NewWebsocketService(opts...), SSE: stream.NewSSEService(opts...),
	})
}

// NewStreamsWith returns the streams on the given services and starts the workers. Call [Streams.Close] to stop them.
func NewStreamsWith(cfg *StreamsConfig) *Streams {
	limit := cfg.MaxPending
	if limit <= 0 {
		limit = DefaultMaxPending
	}

	out := &Streams{ws: cfg.Websocket, sse: cfg.SSE}
	out.lane[0] = startLane(cfg.Label+"/websocket", limit, func(change *flagruntime.Change) {
		// a merged change that undid itself has nothing to tell: the diff is the websocket message
		if !change.Diff.HasDiff() {
			return
		}

		out.ws.BroadcastFlagChanges(change.FlagSet, change.Diff)
	}, out.ws.Close)
	out.lane[1] = startLane(cfg.Label+"/sse", limit, func(change *flagruntime.Change) {
		// a change that undid itself still tells the client to fetch again: it may have fetched the flag meanwhile
		// the SSE service fails only when it can not encode its fixed event, it has nothing to tell the core
		_ = out.sse.BroadcastFlagChanges(change.FlagSet, change.Diff)
	}, out.sse.Close)

	return out
}

// Broadcast queues the change of a flag set for the clients connected to it and returns at once. A change that
// changes no flag is not queued. Changes of one flag set that wait together are merged into one (the SSE event only
// tells the client to fetch again, the websocket message carries the diff, so the diffs are merged to their net
// effect). When more flag sets wait than the bound, the oldest is dropped and counted.
func (s *Streams) Broadcast(change flagruntime.Change) {
	if !change.Diff.HasDiff() {
		return
	}

	for _, l := range s.lane {
		l.offer(&change)
	}
}

// Dropped returns the number of flag set changes that were dropped because a service did not keep up.
func (s *Streams) Dropped() DropCounts {
	return DropCounts{Websocket: s.lane[0].dropped.Load(), SSE: s.lane[1].dropped.Load()}
}

// DropCounts are the flag set changes that a stream service did not keep up with, by service.
type DropCounts struct {
	Websocket, SSE uint64
}

// Close stops the workers and, once a worker is idle, closes its service. It returns when both ended or when ctx ends,
// whichever is first; a worker that is stuck in a delivery closes its service when the delivery returns.
func (s *Streams) Close(ctx context.Context) error {
	for _, l := range s.lane {
		l.close()
	}

	for _, l := range s.lane {
		select {
		case <-l.done:
		case <-ctx.Done():
			return fmt.Errorf("%w: %w", ErrNotStopped, ctx.Err())
		}
	}

	return nil
}

// lane is the bounded queue and the worker of one stream service.
type lane struct {
	pending map[string]*flagruntime.Change
	wake    chan struct{}
	stop    chan struct{}
	done    chan struct{}
	send    func(*flagruntime.Change)
	onExit  func()
	order   []string
	limit   int
	dropped atomic.Uint64
	mu      sync.Mutex
	closed  bool
}

func startLane(label string, limit int, send func(*flagruntime.Change), onExit func()) *lane {
	queue := &lane{
		pending: make(map[string]*flagruntime.Change), wake: make(chan struct{}, 1), stop: make(chan struct{}),
		done: make(chan struct{}), send: send, onExit: onExit, limit: limit,
	}

	go pprof.Do(context.Background(), pprof.Labels("corehttp_streams", label), func(context.Context) { queue.run() })

	return queue
}

func (l *lane) offer(change *flagruntime.Change) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.closed {
		return
	}

	if queued, ok := l.pending[change.FlagSet]; ok {
		l.pending[change.FlagSet] = mergeChanges(queued, change)
	} else {
		if len(l.order) >= l.limit {
			delete(l.pending, l.order[0])
			l.order = l.order[1:]
			l.dropped.Add(1)
		}

		queued := *change
		l.pending[change.FlagSet] = &queued
		l.order = append(l.order, change.FlagSet)
	}

	select {
	case l.wake <- struct{}{}:
	default:
	}
}

func (l *lane) close() {
	l.mu.Lock()
	defer l.mu.Unlock()

	if !l.closed {
		l.closed = true
		close(l.stop)
	}
}

// next takes the oldest queued change.
func (l *lane) next() (*flagruntime.Change, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if len(l.order) == 0 {
		return nil, false
	}

	name := l.order[0]
	l.order = l.order[1:]
	change := l.pending[name]
	delete(l.pending, name)

	return change, true
}

func (l *lane) run() {
	defer close(l.done)
	defer l.onExit()

	for {
		select {
		case <-l.stop:
			return
		case <-l.wake:
		}

		for {
			select {
			case <-l.stop:
				return
			default:
			}

			change, ok := l.next()
			if !ok {
				break
			}

			l.send(change)
		}
	}
}

// keyState is what a diff says of one flag: its value before and after.
type keyState struct {
	before, after       flagdef.Flag
	hadBefore, hasAfter bool
}

// mergeChanges returns the change that has the net effect of the older change followed by the newer one. The
// generation, the snapshot and the removal flag are the newer ones.
func mergeChanges(older, newer *flagruntime.Change) *flagruntime.Change {
	states := map[string]*keyState{}

	record := func(diff *notifier.DiffCache) {
		for key, flag := range diff.Added {
			apply(states, key, false, nil, true, flag)
		}

		for key, flag := range diff.Deleted {
			apply(states, key, true, flag, false, nil)
		}

		for key, upd := range diff.Updated {
			apply(states, key, true, upd.Before, true, upd.After)
		}
	}

	record(&older.Diff)
	record(&newer.Diff)

	merged := *newer
	merged.Diff = notifier.DiffCache{
		Added:   map[string]flagdef.Flag{},
		Deleted: map[string]flagdef.Flag{},
		Updated: map[string]notifier.DiffUpdated{},
	}

	for key, state := range states {
		switch {
		case !state.hadBefore && state.hasAfter:
			merged.Diff.Added[key] = state.after
		case state.hadBefore && !state.hasAfter:
			merged.Diff.Deleted[key] = state.before
		case state.hadBefore && state.hasAfter:
			merged.Diff.Updated[key] = notifier.DiffUpdated{Before: state.before, After: state.after}
		}
	}

	return &merged
}

// apply folds one step of a flag into its state: the first step fixes the value before, the last one the value after.
func apply(states map[string]*keyState, key string, hadBefore bool, before flagdef.Flag, hasAfter bool,
	after flagdef.Flag,
) {
	state, ok := states[key]
	if !ok {
		state = &keyState{hadBefore: hadBefore, before: before}
		states[key] = state
	}

	state.hasAfter, state.after = hasAfter, after
}

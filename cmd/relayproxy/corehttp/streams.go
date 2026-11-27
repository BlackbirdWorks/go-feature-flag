package corehttp

import (
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service/stream"
)

// Streams are the SSE and websocket services of the streams of flag changes. A core calls [Streams.Broadcast] for
// every change (set it as the Broadcast of the dependencies of the core), and the server serves the connections.
type Streams struct {
	ws  *stream.WebsocketServiceImpl
	sse *stream.SSEServiceImpl
}

// NewStreams returns the services of the streams, built with the options of the stream package.
func NewStreams(opts ...stream.Option) *Streams {
	return &Streams{ws: stream.NewWebsocketService(opts...), sse: stream.NewSSEService(opts...)}
}

// Broadcast sends the change of a flag set to the clients connected to its flag set. A change that changes no flag
// is not sent. It never blocks the core on a slow client more than the stream services do.
func (s *Streams) Broadcast(change flagruntime.Change) {
	if !change.Diff.HasDiff() {
		return
	}

	s.ws.BroadcastFlagChanges(change.FlagSet, change.Diff)
	// the SSE service fails only when it can not encode its fixed event, it has nothing to tell the core
	_ = s.sse.BroadcastFlagChanges(change.FlagSet, change.Diff)
}

// Close closes the connections of the clients.
func (s *Streams) Close() {
	s.ws.Close()
	s.sse.Close()
}

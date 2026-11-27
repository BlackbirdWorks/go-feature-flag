package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
)

// ErrCut is wrapped by the error of [Server.Shutdown] when the context ended before the servers stopped: they were
// closed hard and the requests still in flight were cut.
var ErrCut = errors.New("the server was closed hard, the requests in flight were cut")

// shutdownState is what [Server.Shutdown] hands to the listeners: the context that bounds the graceful stop, and
// what it cut.
type shutdownState struct {
	ctx atomic.Pointer[context.Context]
	cut atomic.Int64
	// started is true once [Server.StartWithContext] ran: only then does stopped close.
	started atomic.Bool
}

// context returns the context given to [Server.Shutdown]; without one (the server is stopped by its own context) the
// graceful stop is bounded by [gracefulShutdownTimeout].
func (st *shutdownState) context(parent context.Context) (context.Context, context.CancelFunc) {
	given := st.ctx.Load()
	if given != nil {
		return *given, func() {}
	}

	return context.WithTimeout(context.WithoutCancel(parent), gracefulShutdownTimeout)
}

func (st *shutdownState) addCut(n int)  { st.cut.Add(int64(n)) }
func (st *shutdownState) cutCount() int { return int(st.cut.Load()) }

// connTracker counts the connections that are serving a request.
type connTracker struct {
	states map[net.Conn]http.ConnState
	mu     sync.Mutex
}

// track is the ConnState hook of the servers.
func (c *connTracker) track(conn net.Conn, state http.ConnState) {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch state {
	case http.StateNew, http.StateActive, http.StateIdle:
		c.states[conn] = state
	case http.StateHijacked, http.StateClosed:
		delete(c.states, conn)
	}
}

// active is the number of connections with a request in flight.
func (c *connTracker) active() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	count := 0

	for _, state := range c.states {
		if state == http.StateActive {
			count++
		}
	}

	return count
}

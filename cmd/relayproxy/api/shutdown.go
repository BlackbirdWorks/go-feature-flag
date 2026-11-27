package api

import (
	"context"
	"errors"
	"net/http"
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
	// serving is true once the initialisation of [Server.StartWithContext] ended: the servers answer a stop then.
	serving atomic.Bool
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

// requestCounter counts the requests that one server is serving: every listener has its own, so that a request is
// counted once.
type requestCounter struct {
	inFlight atomic.Int64
}

// wrap returns the handler with the count of the requests in flight.
func (c *requestCounter) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.inFlight.Add(1)
		defer c.inFlight.Add(-1)

		next.ServeHTTP(w, r)
	})
}

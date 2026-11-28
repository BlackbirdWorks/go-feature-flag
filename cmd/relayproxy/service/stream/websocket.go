package stream

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/blackbirdworks/semaphore/notifier"
)

// WebsocketConnector is an interface to be able to mock websocket.Conn.
type WebsocketConnector interface {
	WriteJSON(v any) error
}

// WebsocketService is the service interface that handle the websocket connections
// This service is able to broadcast a notification to all the open websockets.
type WebsocketService interface {
	// Register is adding the connection to the list of open connection of the flagset.
	Register(c WebsocketConnector, flagsetName string)
	// RegisterAnonymous is Register for a connection that has no API key: the flag set is the public flag set of
	// the relay, and [WebsocketService.DisconnectAnonymous] closes the connection when it is not anymore.
	RegisterAnonymous(c WebsocketConnector, flagsetName string)
	// RegisterKeyed is Register for a connection that authenticated with an API key: the service keeps an identifier of
	// the key (never the key), so that [WebsocketService.DisconnectRevoked] can close the connection when the key does
	// not route to the flag set anymore.
	RegisterKeyed(c WebsocketConnector, flagsetName, apiKey string)
	// DisconnectRevoked takes out of the list, before it returns, the connections whose key is not in routes anymore or
	// routes to another flag set than the one of the connection, and returns what closes them: the caller runs it away
	// from the swap, a close can take time. Connections without a key are not touched.
	DisconnectRevoked(routes KeyRoutes) []func()
	// DisconnectAnonymous closes the connections without a key whose flag set is not keep; an empty keep closes all
	// of them. A connection with a key is never touched.
	DisconnectAnonymous(keep string)
	// Deregister is removing the connection from the list of open connection.
	Deregister(c WebsocketConnector)
	// Connections is the number of open connections.
	Connections() int
	// BroadcastFlagChanges is sending the diff cache struct to the clients of the flagset.
	BroadcastFlagChanges(flagsetName string, diff notifier.DiffCache)
	// Close deregister all open connections.
	Close()
	// WaitForCleanup waits for all connections to be properly closed
	WaitForCleanup(timeout time.Duration) error
}

// WebsocketServiceImpl is the implementation of the interface.
type WebsocketServiceImpl struct {
	clients            map[WebsocketConnector]string
	anonymous          map[WebsocketConnector]struct{}
	keyed              map[WebsocketConnector]uint64
	mutex              *sync.RWMutex
	closed             chan struct{}
	hasher             keyHasher
	writeTimeout       time.Duration
	includeFlagDetails bool
}

// NewWebsocketService is a constructor to create a new WebsocketServiceImpl.
func NewWebsocketService(opts ...Option) *WebsocketServiceImpl {
	streamOpts := newOptions(opts...)

	return &WebsocketServiceImpl{
		clients:            map[WebsocketConnector]string{},
		anonymous:          map[WebsocketConnector]struct{}{},
		keyed:              map[WebsocketConnector]uint64{},
		hasher:             newKeyHasher(),
		mutex:              &sync.RWMutex{},
		closed:             make(chan struct{}),
		includeFlagDetails: streamOpts.includeFlagDetails,
		writeTimeout:       streamOpts.writeTimeout,
	}
}

// BroadcastFlagChanges is sending the diff to all the open connection of the flagset. No lock is held while a client
// is written to: the recipients are taken under the lock, then every one is written to on its own, and one that does
// not take the message within the write timeout is closed and removed. The others still get the change.
func (w *WebsocketServiceImpl) BroadcastFlagChanges(flagsetName string, diff notifier.DiffCache) {
	w.mutex.RLock()

	recipients := make([]WebsocketConnector, 0, len(w.clients))

	for conn, clientFlagsetName := range w.clients {
		if clientFlagsetName == flagsetName {
			recipients = append(recipients, conn)
		}
	}

	w.mutex.RUnlock()

	payload := flagChangePayload(diff, w.includeFlagDetails)

	var group sync.WaitGroup

	for _, conn := range recipients {
		group.Go(func() {
			if !w.write(conn, payload) {
				w.drop(conn)
			}
		})
	}

	group.Wait()
}

// Register is adding the connection to the list of open connection of the flagset.
func (w *WebsocketServiceImpl) Register(c WebsocketConnector, flagsetName string) {
	w.mutex.Lock()
	defer w.mutex.Unlock()

	w.clients[c] = flagsetName
}

// RegisterAnonymous is Register for a connection that has no API key.
func (w *WebsocketServiceImpl) RegisterAnonymous(c WebsocketConnector, flagsetName string) {
	w.mutex.Lock()
	defer w.mutex.Unlock()

	w.clients[c] = flagsetName
	w.anonymous[c] = struct{}{}
}

// RegisterKeyed is Register for a connection that has an API key.
func (w *WebsocketServiceImpl) RegisterKeyed(conn WebsocketConnector, flagsetName, apiKey string) {
	id := w.hasher.id(apiKey)

	w.mutex.Lock()
	defer w.mutex.Unlock()

	w.clients[conn] = flagsetName
	w.keyed[conn] = id
}

// DisconnectRevoked takes the connections of the keys that do not route to their flag set anymore out of the list, and
// returns the closes. Nothing is written to a socket under the lock, and a service with no keyed connection does not
// read the routes.
func (w *WebsocketServiceImpl) DisconnectRevoked(routes KeyRoutes) []func() {
	w.mutex.RLock()
	keyed := len(w.keyed) > 0
	w.mutex.RUnlock()

	if !keyed {
		return nil
	}

	live := w.hasher.liveIDs(routes)

	w.mutex.Lock()
	defer w.mutex.Unlock()

	var closers []func()

	for conn, id := range w.keyed {
		if set, ok := live[id]; ok && set == w.clients[conn] {
			continue
		}

		delete(w.clients, conn)
		delete(w.keyed, conn)

		closers = append(closers, func() { closeConn(conn) })
	}

	return closers
}

func closeConn(conn WebsocketConnector) {
	if closer, ok := conn.(interface{ Close() error }); ok {
		_ = closer.Close()
	}
}

// DisconnectAnonymous closes the connections without a key whose flag set is not keep. They are out of the list
// before it returns, so no change reaches them anymore.
func (w *WebsocketServiceImpl) DisconnectAnonymous(keep string) {
	w.mutex.Lock()

	var revoked []WebsocketConnector

	for conn := range w.anonymous {
		if w.clients[conn] != keep || keep == "" {
			revoked = append(revoked, conn)

			delete(w.clients, conn)
			delete(w.anonymous, conn)
		}
	}

	w.mutex.Unlock()

	for _, conn := range revoked {
		if closer, ok := conn.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
	}
}

// Deregister is removing the connection from the list of open connection.
func (w *WebsocketServiceImpl) Deregister(conn WebsocketConnector) {
	w.mutex.Lock()
	defer w.mutex.Unlock()

	delete(w.clients, conn)
	delete(w.anonymous, conn)
	delete(w.keyed, conn)
}

// Connections is the number of open connections.
func (w *WebsocketServiceImpl) Connections() int {
	w.mutex.RLock()
	defer w.mutex.RUnlock()

	return len(w.clients)
}

// Close deregister all open connections.
func (w *WebsocketServiceImpl) Close() {
	w.mutex.Lock()
	defer w.mutex.Unlock()

	for c := range w.clients {
		delete(w.clients, c)
	}

	clear(w.anonymous)
	clear(w.keyed)

	close(w.closed)
}

// WaitForCleanup waits for all connections to be properly closed.
func (w *WebsocketServiceImpl) WaitForCleanup(timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	select {
	case <-w.closed:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("waiting for the cleanup of the websocket connections: %w", ctx.Err())
	}
}

// write sends the payload to a client that is still registered, and reports whether the client took it in time.
// A client that left meanwhile (a revoked access) gets nothing and is not a failure.
func (w *WebsocketServiceImpl) write(conn WebsocketConnector, payload any) bool {
	w.mutex.RLock()
	_, registered := w.clients[conn]
	w.mutex.RUnlock()

	if !registered {
		return true
	}

	result := make(chan error, 1)

	go func() { result <- conn.WriteJSON(payload) }()

	timer := time.NewTimer(w.writeTimeout)
	defer timer.Stop()

	select {
	case err := <-result:
		return err == nil
	case <-timer.C:
		return false
	}
}

// drop removes a client and closes its connection, which ends a write that is stuck on it.
func (w *WebsocketServiceImpl) drop(conn WebsocketConnector) {
	w.Deregister(conn)

	if closer, ok := conn.(interface{ Close() error }); ok {
		_ = closer.Close()
	}
}

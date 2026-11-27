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
	// RegisterAnonymous is Register for a connection that has no API key: the flag set is the anonymous flag set of
	// the relay, and [WebsocketService.DisconnectAnonymous] closes the connection when it is not anymore.
	RegisterAnonymous(c WebsocketConnector, flagsetName string)
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
	// clients stores the flagset name of every open connection.
	clients map[WebsocketConnector]string
	// anonymous is the connections that have no API key.
	anonymous          map[WebsocketConnector]struct{}
	mutex              *sync.RWMutex
	closed             chan struct{}
	includeFlagDetails bool
}

// NewWebsocketService is a constructor to create a new WebsocketServiceImpl.
func NewWebsocketService(opts ...Option) *WebsocketServiceImpl {
	streamOpts := newOptions(opts...)

	return &WebsocketServiceImpl{
		clients:            map[WebsocketConnector]string{},
		anonymous:          map[WebsocketConnector]struct{}{},
		mutex:              &sync.RWMutex{},
		closed:             make(chan struct{}),
		includeFlagDetails: streamOpts.includeFlagDetails,
	}
}

// BroadcastFlagChanges is sending the diff to all the open connection of the flagset.
func (w *WebsocketServiceImpl) BroadcastFlagChanges(flagsetName string, diff notifier.DiffCache) {
	w.mutex.RLock()
	defer w.mutex.RUnlock()

	payload := flagChangePayload(diff, w.includeFlagDetails)
	for conn, clientFlagsetName := range w.clients {
		if clientFlagsetName != flagsetName {
			continue
		}

		err := conn.WriteJSON(payload)
		if err != nil {
			w.mutex.RUnlock()
			w.Deregister(conn)
			w.mutex.RLock()
		}
	}
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
func (w *WebsocketServiceImpl) Deregister(c WebsocketConnector) {
	w.mutex.Lock()
	defer w.mutex.Unlock()

	delete(w.clients, c)
	delete(w.anonymous, c)
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

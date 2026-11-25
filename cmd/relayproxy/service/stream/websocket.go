package stream

import (
	"context"
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
	// Deregister is removing the connection from the list of open connection.
	Deregister(c WebsocketConnector)
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
	clients            map[WebsocketConnector]string
	mutex              *sync.RWMutex
	closed             chan struct{}
	includeFlagDetails bool
}

// NewWebsocketService is a constructor to create a new WebsocketServiceImpl.
func NewWebsocketService(opts ...Option) *WebsocketServiceImpl {
	streamOpts := newOptions(opts...)

	return &WebsocketServiceImpl{
		clients:            map[WebsocketConnector]string{},
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

// Deregister is removing the connection from the list of open connection.
func (w *WebsocketServiceImpl) Deregister(c WebsocketConnector) {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	delete(w.clients, c)
}

// Close deregister all open connections.
func (w *WebsocketServiceImpl) Close() {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	for c := range w.clients {
		delete(w.clients, c)
	}
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
		return ctx.Err()
	}
}

package proxynotifier

import (
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service/stream"
	"github.com/blackbirdworks/semaphore/notifier"
)

// NotifierWebsocket pushes flag changes to the WebSocket streams of a flagset.
type NotifierWebsocket struct {
	websocketService stream.WebsocketService
	flagsetName      string
}

// NewNotifierWebsocket returns a notifier that pushes flag changes to the WebSocket streams of flagsetName.
func NewNotifierWebsocket(websocketService stream.WebsocketService, flagsetName string) *NotifierWebsocket {
	return &NotifierWebsocket{
		websocketService: websocketService,
		flagsetName:      flagsetName,
	}
}

// Notify sends the flag changes of diff to the WebSocket streams.
func (n *NotifierWebsocket) Notify(diff notifier.DiffCache) error {
	n.websocketService.BroadcastFlagChanges(n.flagsetName, diff)

	return nil
}

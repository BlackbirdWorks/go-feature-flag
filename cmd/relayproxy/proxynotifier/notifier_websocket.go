package proxynotifier

import (
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service/stream"
	"github.com/blackbirdworks/semaphore/notifier"
)

type NotifierWebsocket struct {
	websocketService stream.WebsocketService
	flagsetName      string
}

func NewNotifierWebsocket(websocketService stream.WebsocketService, flagsetName string) *NotifierWebsocket {
	return &NotifierWebsocket{
		websocketService: websocketService,
		flagsetName:      flagsetName,
	}
}

func (n *NotifierWebsocket) Notify(diff notifier.DiffCache) error {
	n.websocketService.BroadcastFlagChanges(n.flagsetName, diff)

	return nil
}

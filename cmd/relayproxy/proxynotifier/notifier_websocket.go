package proxynotifier

import (
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service/stream"
	"github.com/blackbirdworks/semaphore/notifier"
)

type notifierWebsocket struct {
	websocketService stream.WebsocketService
	flagsetName      string
}

func NewNotifierWebsocket(websocketService stream.WebsocketService, flagsetName string) notifier.Notifier {
	return &notifierWebsocket{
		websocketService: websocketService,
		flagsetName:      flagsetName,
	}
}

func (n *notifierWebsocket) Notify(diff notifier.DiffCache) error {
	n.websocketService.BroadcastFlagChanges(n.flagsetName, diff)

	return nil
}

package proxynotifier

import (
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service/stream"
	"github.com/blackbirdworks/semaphore/notifier"
)

type NotifierSSE struct {
	sseService  stream.SSEService
	flagsetName string
}

// NewNotifierSSE creates a notifier that forwards flag change events to the
// SSE service scoped to the given flagset name.
func NewNotifierSSE(sseService stream.SSEService, flagsetName string) *NotifierSSE {
	return &NotifierSSE{
		sseService:  sseService,
		flagsetName: flagsetName,
	}
}

func (n *NotifierSSE) Notify(diff notifier.DiffCache) error {
	return n.sseService.BroadcastFlagChanges(n.flagsetName, diff)
}

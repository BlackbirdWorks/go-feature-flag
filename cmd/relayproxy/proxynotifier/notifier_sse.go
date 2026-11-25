package proxynotifier

import (
	"fmt"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service/stream"
	"github.com/blackbirdworks/semaphore/notifier"
)

// NotifierSSE pushes flag changes to the SSE streams.
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

// Notify sends the flag changes of diff to the SSE streams.
func (n *NotifierSSE) Notify(diff notifier.DiffCache) error {
	err := n.sseService.BroadcastFlagChanges(n.flagsetName, diff)
	if err != nil {
		return fmt.Errorf("broadcast SSE flag changes: %w", err)
	}

	return nil
}

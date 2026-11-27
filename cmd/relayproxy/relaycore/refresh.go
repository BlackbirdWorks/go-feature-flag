package relaycore

import (
	"context"
	"fmt"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/internal/manifestreader"
)

// RefreshResult is what a refresh found: the poll of the reader, and what the engine decided of the generation the
// poll left the reader with.
type RefreshResult struct {
	// Poll is the outcome of the poll.
	Poll manifestreader.RefreshResult
	// Engine is the outcome of the engine for that generation: applied (the relay serves it or a later one) or
	// rejected, with its code.
	Engine flagruntime.Reload
}

// Refresh polls the control plane now, through the loop that polls on schedule, then waits until the engine has
// decided the generation the poll left: what the relay serves is known when it returns. Calls that arrive together
// share one poll, and ctx bounds both waits.
func (c *Core) Refresh(ctx context.Context) (RefreshResult, error) {
	polled, err := c.reader.Refresh(ctx)
	if err != nil {
		return RefreshResult{}, fmt.Errorf("refresh: %w", err)
	}

	decided, err := c.engine.Await(ctx, polled.Generation)
	if err != nil {
		return RefreshResult{}, fmt.Errorf("refresh: %w", err)
	}

	return RefreshResult{Poll: polled, Engine: decided}, nil
}

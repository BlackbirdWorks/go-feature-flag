package manifestreader

import (
	"context"
	"fmt"
	"time"
)

// Scheduler is the clock seam of the polling loop: the one place that waits. Tests drive the rounds with a
// scheduler of their own, so no test depends on a real timer.
type Scheduler interface {
	// Wait blocks for d, or until ctx is done; it then returns the error of ctx.
	Wait(ctx context.Context, d time.Duration) error
}

// timerScheduler is the scheduler of production: a real timer.
type timerScheduler struct{}

func (timerScheduler) Wait(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return fmt.Errorf("manifestreader: wait: %w", ctx.Err())
	case <-timer.C:
		return nil
	}
}

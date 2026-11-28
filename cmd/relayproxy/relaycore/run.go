package relaycore

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/internal/connection"
)

// Run starts the loops (the reader, the source, the engine, the notification worker and the state directory
// writer) and blocks until ctx is done or [Core.Shutdown] is called. It returns nil after the loops ended and the
// state directory wrote what it held. It does not stop the exporters and notifiers: [Core.Shutdown] drains them,
// and must always be called. Run can be called once, and not after Shutdown.
func (c *Core) Run(ctx context.Context) error {
	c.mu.Lock()

	switch c.phase {
	case phaseRunning:
		c.mu.Unlock()

		return ErrAlreadyRunning
	case phaseClosed:
		c.mu.Unlock()

		return ErrClosed
	case phaseNew:
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	c.cancel = cancel
	c.phase = phaseRunning
	c.mu.Unlock()

	defer close(c.runDone)

	// the exporters and notifiers outlive the loops: only Shutdown ends them, with its own deadline
	err := c.events.Start(context.WithoutCancel(ctx), c.engine.Changes())
	if err != nil {
		return fmt.Errorf("start the events: %w", err)
	}

	var loops sync.WaitGroup

	loops.Go(func() { c.engine.Run(runCtx) })
	loops.Go(func() { c.source.Run(runCtx) })
	loops.Go(func() {
		readErr := c.reader.Run(runCtx)
		if readErr != nil {
			c.log.LogAttrs(runCtx, slog.LevelError, "the manifest reader stopped", slog.Any("error", readErr))
		}
	})

	// the writer of the state directory outlives the reader: it writes the last snapshot before it returns
	stateStop := c.runStateWriter(ctx)

	<-runCtx.Done()
	loops.Wait()
	stateStop()

	return nil
}

// runStateWriter starts the writer of the state directory, if there is one, and returns the function that stops it
// and waits for its last write.
func (c *Core) runStateWriter(ctx context.Context) func() {
	if c.state == nil {
		return func() {}
	}

	writerCtx, stop := context.WithCancel(context.WithoutCancel(ctx))

	var writer sync.WaitGroup

	writer.Go(func() { c.state.Run(writerCtx) })

	return func() {
		stop()
		writer.Wait()
	}
}

// Reload applies a new bootstrap file in place. Only the admin keys apply live; every
// other change is kept out and named in the PendingRestart of the status (the connections, the control plane, the
// exporters, the notifiers, the listeners). The reload is asynchronous and latest-wins: the engine takes the file in
// its next round, and the outcome is in the status. cfg must be a loaded and validated file, and belongs to the core
// afterwards.
func (c *Core) Reload(cfg *bootstrap.Config) error {
	if cfg == nil {
		return ErrNilConfig
	}

	if cfg.ControlPlane == nil {
		return &BuildError{Path: "controlPlane", Err: ErrNoControlPlane}
	}

	c.mu.Lock()
	closed := c.phase == phaseClosed
	c.mu.Unlock()

	if closed {
		return ErrClosed
	}

	c.engine.Reload(cfg)

	return nil
}

// Shutdown stops the core in order: it stops the reader, the source and the engine (nothing new enters the
// runtime), waits for the state directory to write what it holds, drains the exporters and notifiers until the
// deadline of ctx, and releases the connections. It is safe to call without Run, and more than once: a later
// call waits for the first and returns its result. When ctx ends first, Shutdown returns an error that wraps
// [ErrNotDrained] and the context error, and what did not stop keeps stopping in the background: a goroutine of
// the core then releases the state directory and the collector once the loops ended. A later call waits for that
// cleanup within its own context.
func (c *Core) Shutdown(ctx context.Context) error {
	if !c.shutdownStarted.CompareAndSwap(false, true) {
		return c.awaitFirst(ctx)
	}

	defer close(c.finished)

	c.shutdownErr = c.stopAll(ctx)

	return c.shutdownErr
}

// awaitFirst is what a later Shutdown does: it waits, within its own context, for the first call and for the
// cleanup that follows the loops, and returns the result of the first call.
func (c *Core) awaitFirst(ctx context.Context) error {
	for _, done := range []<-chan struct{}{c.finished, c.released} {
		select {
		case <-done:
		case <-ctx.Done():
			return fmt.Errorf("%w: %w", ErrNotDrained, context.Cause(ctx))
		}
	}

	return c.shutdownErr
}

func (c *Core) stopAll(ctx context.Context) error {
	c.mu.Lock()
	wasRunning := c.phase == phaseRunning
	c.phase = phaseClosed
	cancel := c.cancel
	c.mu.Unlock()

	var errs []error

	loopsDone := true

	if wasRunning {
		cancel()

		select {
		case <-c.runDone:
		case <-ctx.Done():
			loopsDone = false

			errs = append(errs, fmt.Errorf("%w: the loops: %w", ErrNotDrained, context.Cause(ctx)))
		}
	}

	_, err := c.events.Shutdown(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("drain the events: %w", err))
	}

	// the state directory and the service belong to the loops: they are closed only after the loops ended
	if loopsDone {
		c.release(ctx)

		return errors.Join(errs...)
	}

	// the loops are still stopping: the cleanup runs once, when they have, in a goroutine of the core. Run closes
	// runDone, and it was called, since the loops are not done.
	go func() {
		<-c.runDone
		c.release(context.WithoutCancel(ctx))
	}()

	return errors.Join(errs...)
}

// release frees what the core holds besides the loops: the collector, the state directory, the idle connections
// and the flag service. It runs after the loops ended (or when New failed before they could start).
func (c *Core) release(ctx context.Context) {
	c.releaseOnce.Do(func() {
		defer close(c.released)

		c.releaseAll(ctx)
	})
}

func (c *Core) releaseAll(ctx context.Context) {
	if c.collector != nil && c.registry != nil {
		c.registry.Unregister(c.collector)
	}

	if c.readerMetrics != nil {
		c.registry.Unregister(c.readerMetrics)
	}

	if c.events != nil {
		_, _ = c.events.Shutdown(ctx)
	}

	if c.state != nil {
		err := c.state.Close()
		if err != nil {
			c.log.LogAttrs(ctx, slog.LevelError, "close the state directory", slog.Any("error", err))
		}
	}

	_ = c.factory.Replace(map[string]connection.Spec{})

	if c.svc != nil {
		c.svc.Close()
	}
}

package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/blackbirdworks/semaphore/cmdhelpers/cmdlog"
)

// gracefulShutdownTimeout is the time given to in-flight requests to complete when the servers are stopped.
const gracefulShutdownTimeout = 5 * time.Second

// defaultReadTimeout is how long the server waits for a request to be read, its body included (a defence against
// slow clients). WithReadTimeout changes it.
const defaultReadTimeout = 30 * time.Second

// StartWithContext starts the server and stops it when ctx is done.
func (s *Server) StartWithContext(ctx context.Context) {
	defer close(s.stopped)

	s.shutdown.started.Store(true)

	if s.stopWasRequested() {
		return
	}

	// The server owns no signal handler: the process does, so that it can fail the readiness probe before the
	// listeners close. Stop ends s.stopCtx, which cancels ctx and gracefully shuts down the servers below.
	// The watcher is installed before anything slow runs, so that a stop reaches the initialisation too.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	stopWatching := context.AfterFunc(s.stopCtx, cancel) //nolint:contextcheck // the stop request is the server's own
	defer stopWatching()

	// start the OpenTelemetry tracing service
	err := s.otelService.Init(ctx, &s.settings.Telemetry)
	if err != nil {
		s.logger.LogAttrs(ctx, slog.LevelError,
			"error while initializing OTel, continuing without tracing enabled",
			cmdlog.Err(err),
		)
		// we can continue because otel is not mandatory to start the server
	}

	if err == nil && s.installGlobalTelemetry {
		s.otelService.Install(ctx, s.logger)
	}

	if s.startupStep != nil {
		s.startupStep(ctx)
	}

	// from here a stop is answered by the servers themselves, which end promptly: Shutdown may wait for them
	s.shutdown.serving.Store(true)

	if s.stopWasRequested() {
		return
	}

	switch s.settings.Mode {
	case ServerModeUnixSocket:
		s.startUnixSocketServer(ctx)
	default:
		s.startAsHTTPServer(ctx)
	}
}

// stopWasRequested reports whether Stop or Shutdown ran; the listeners that the server was given are closed then,
// nothing will serve on them.
func (s *Server) stopWasRequested() bool {
	if s.stopCtx.Err() == nil {
		return false
	}

	for _, l := range []net.Listener{s.listener, s.monitoringListener} {
		if l != nil {
			_ = l.Close()
		}
	}

	return true
}

// startUnixSocketServer launch the API server as a unix socket.
func (s *Server) startUnixSocketServer(ctx context.Context) {
	socketPath := s.settings.UnixSocketPath

	// Clean up the old socket file if it exists (important for graceful restarts)
	_, err := os.Stat(socketPath)
	if err == nil {
		removeErr := os.Remove(socketPath)
		if removeErr != nil {
			s.logger.LogAttrs(ctx, cmdlog.LevelFatal, "Could not remove old socket file",
				slog.String("path", socketPath), cmdlog.Err(removeErr))
			s.exit()
		}
	}

	// Start a http server for monitoring if monitoringport is configured
	if s.isMonitoringPortConfigured() {
		var wg sync.WaitGroup

		wg.Go(func() { s.startMonitoringServer(ctx) })
		defer wg.Wait()
	}

	lc := net.ListenConfig{}

	listener, err := lc.Listen(ctx, "unix", socketPath)
	if err != nil {
		s.logger.LogAttrs(ctx, cmdlog.LevelFatal, "Error creating Unix listener", cmdlog.Err(err))
		s.exit()
	}

	defer func() {
		// the graceful shutdown already closes the listener
		closeErr := listener.Close()
		if closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
			s.logger.LogAttrs(
				context.WithoutCancel(ctx),
				slog.LevelError,
				"error closing unix socket listener",
				cmdlog.Err(closeErr),
			)
		}
	}()

	s.logger.LogAttrs(ctx, slog.LevelInfo,
		"Starting semaphore relay proxy as unix socket...",
		slog.String("socket", socketPath),
		slog.String("version", s.settings.Version))

	err = s.serve(ctx, s.apiHandler, listener, false, "api")
	if err != nil {
		s.logger.LogAttrs(ctx, cmdlog.LevelFatal, "Error starting relay proxy as unix socket", cmdlog.Err(err))
		s.exit()
	}
}

// startAsHTTPServer launch the API server.
func (s *Server) startAsHTTPServer(ctx context.Context) {
	if s.isMonitoringPortConfigured() {
		var wg sync.WaitGroup

		wg.Go(func() { s.startMonitoringServer(ctx) })
		defer wg.Wait()
	}

	address := net.JoinHostPort(s.settings.Host, strconv.Itoa(s.settings.Port))
	s.logger.LogAttrs(ctx, slog.LevelInfo,
		"Starting semaphore relay proxy ...",
		slog.String("address", address),
		slog.String("version", s.settings.Version))

	// serve blocks until ctx is cancelled and the graceful shutdown has drained the connections.
	listener, err := s.listenOn(ctx, address, s.listener)
	if err == nil {
		err = s.serve(ctx, s.apiHandler, listener, s.settings.EnableH2C, "api")
	}

	if err != nil {
		s.logger.LogAttrs(ctx, cmdlog.LevelFatal, "Error starting relay proxy", cmdlog.Err(err))
		s.exit()
	}
}

// listenOn returns the listener the server was given, or else listens on the address.
func (s *Server) listenOn(ctx context.Context, address string, given net.Listener) (net.Listener, error) {
	if given != nil {
		return given, nil
	}

	listen := s.listen
	if listen == nil {
		listen = (&net.ListenConfig{}).Listen
	}

	listener, err := listen(ctx, "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", address, err)
	}

	return listener, nil
}

// serve serves the handler on the listener until ctx is done, then it gracefully shuts the server down: the
// connections get gracefulShutdownTimeout to complete. With h2c, HTTP/2 over cleartext (prior knowledge) is served
// next to HTTP/1.1; without it the server keeps net/http's default (HTTP/1.1 only).
func (s *Server) serve(ctx context.Context, h http.Handler, listener net.Listener, h2c bool, serverName string) error {
	counter := &requestCounter{}

	srv := &http.Server{
		Handler:  counter.wrap(h),
		ErrorLog: slog.NewLogLogger(s.logger.Handler(), slog.LevelError),
		// ReadTimeout is a defence against slow clients (gosec G112). There is no WriteTimeout: the streams
		// (SSE, websocket) are long-lived.
		ReadTimeout: s.readTimeout,
	}

	if h2c {
		var p http.Protocols
		p.SetHTTP1(true)
		p.SetUnencryptedHTTP2(true)
		srv.Protocols = &p
	}

	// the shutdown goroutine ends when ctx is done or when Serve returned (the cancel below): a listener that
	// fails must not wait for an external shutdown before its error reaches the caller.
	gCtx, cancel := context.WithCancel(ctx)

	var wg sync.WaitGroup

	defer func() {
		cancel()
		wg.Wait()
	}()

	wg.Go(func() {
		<-gCtx.Done()

		shutdownCtx, cancelShutdown := s.shutdown.context(gCtx)
		defer cancelShutdown()

		err := srv.Shutdown(shutdownCtx)
		if err != nil {
			// the shutdown context ended: the requests still in flight are cut
			cut := int(counter.inFlight.Load())
			_ = srv.Close()

			s.shutdown.addCut(cut)
			s.logger.LogAttrs(context.WithoutCancel(gCtx), slog.LevelError, "Error shutting down the server, cut",
				slog.String("server", serverName), slog.Int("inFlightRequests", cut), cmdlog.Err(err))
		}
	})

	err := srv.Serve(listener)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve HTTP: %w", err)
	}

	return nil
}

func (s *Server) startMonitoringServer(ctx context.Context) {
	addressMonitoring := net.JoinHostPort(s.settings.Host, strconv.Itoa(s.settings.MonitoringPort))
	s.logger.LogAttrs(ctx, slog.LevelInfo,
		"Starting monitoring",
		slog.String("address", addressMonitoring))

	listener, err := s.listenOn(ctx, addressMonitoring, s.monitoringListener)
	if err == nil {
		err = s.serve(ctx, s.monitoringHandler, listener, false, "monitoring")
	}

	if err != nil {
		s.logger.LogAttrs(ctx, cmdlog.LevelFatal, "Error starting monitoring", cmdlog.Err(err))
		s.exit()
	}
}

// Stop shutdown the API server.
// It waits for StartWithContext to return (servers drained) or for ctx to be done.
func (s *Server) Stop(ctx context.Context) { _ = s.Shutdown(ctx) }

// Shutdown stops the servers gracefully: the listeners close and the requests in flight finish. When ctx ends first
// the servers close hard, the requests still in flight are cut, and the error says so (it wraps [ErrCut]).
func (s *Server) Shutdown(ctx context.Context) error {
	s.shutdown.ctx.Store(&ctx)

	err := s.otelService.Stop(ctx)
	if err != nil {
		s.logger.LogAttrs(context.WithoutCancel(ctx), slog.LevelError, "impossible to stop otel", cmdlog.Err(err))
	}

	s.stop()

	// StartWithContext records that it started before it looks at stopCtx, and Shutdown closes stopCtx
	// before it looks at started: whichever comes second sees the other. A server that never started has nothing to
	// wait for, and a start that comes later returns at once because stopCtx is closed.
	if !s.shutdown.started.Load() {
		return nil
	}

	select {
	case <-s.stopped:
	case <-ctx.Done():
		s.logger.LogAttrs(
			context.WithoutCancel(ctx), slog.LevelError, "error stopping relay proxy", cmdlog.Err(ctx.Err()))

		// Once the servers serve they close hard as soon as the context is done, which is prompt. While the
		// initialisation still runs nothing answers the stop: Shutdown does not wait for it.
		if s.shutdown.serving.Load() {
			<-s.stopped
		}

		return fmt.Errorf("%w: %d request(s) in flight: %w", ErrCut, s.shutdown.cutCount(), ctx.Err())
	}

	cut := s.shutdown.cutCount()
	if cut > 0 {
		return fmt.Errorf("%w: %d request(s) in flight", ErrCut, cut)
	}

	return nil
}

// isMonitoringPortConfigured checks if the monitoring port is configured.
func (s *Server) isMonitoringPortConfigured() bool {
	return s.monitoringHandler != nil && s.settings.MonitoringPort > 0
}

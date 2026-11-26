package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/aws/aws-lambda-go/lambda"

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

	select {
	case <-s.stopRequested:
		// Stop has been called before the server started: the server owns its listener, nothing will serve on it.
		for _, l := range []net.Listener{s.listener, s.monitoringListener} {
			if l != nil {
				_ = l.Close()
			}
		}

		return
	default:
	}

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

	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()
	// Stop closes s.stopRequested, which cancels ctx and gracefully shuts down the servers started below.
	go func() {
		select {
		case <-s.stopRequested:
			cancel()
		case <-ctx.Done():
		}
	}()

	switch s.settings.Mode {
	case ServerModeLambda:
		s.startAwsLambda()
	case ServerModeUnixSocket:
		s.startUnixSocketServer(ctx)
	default:
		s.startAsHTTPServer(ctx)
	}
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

	address := fmt.Sprintf("%s:%d", s.settings.Host, s.settings.Port)
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
func (*Server) listenOn(ctx context.Context, address string, given net.Listener) (net.Listener, error) {
	if given != nil {
		return given, nil
	}

	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", address, err)
	}

	return listener, nil
}

// serve serves the handler on the listener until ctx is done, then it gracefully shuts the server down: the
// connections get gracefulShutdownTimeout to complete. With h2c, HTTP/2 over cleartext (prior knowledge) is served
// next to HTTP/1.1; without it the server keeps net/http's default (HTTP/1.1 only).
func (s *Server) serve(ctx context.Context, h http.Handler, listener net.Listener, h2c bool, serverName string) error {
	srv := &http.Server{
		Handler:  h,
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

		shutdownCtx, cancelShutdown := context.WithTimeout(context.WithoutCancel(gCtx), gracefulShutdownTimeout)
		defer cancelShutdown()

		err := srv.Shutdown(shutdownCtx)
		if err != nil {
			s.logger.LogAttrs(context.WithoutCancel(gCtx), slog.LevelError, "Error shutting down the server",
				slog.String("server", serverName), cmdlog.Err(err))
		}
	})

	err := srv.Serve(listener)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve HTTP: %w", err)
	}

	return nil
}

func (s *Server) startMonitoringServer(ctx context.Context) {
	addressMonitoring := fmt.Sprintf("%s:%d", s.settings.Host, s.settings.MonitoringPort)
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

// startAwsLambda is starting the relay proxy as an AWS Lambda.
func (s *Server) startAwsLambda() {
	lambda.Start(s.lambdaHandler())
}

// lambdaHandler returns the appropriate lambda handler based on the configuration.
// We need a dedicated function because it is called from tests as well, this is the
// reason why we can't merged it in startAwsLambda.
func (s *Server) lambdaHandler() any {
	handlerMngr := newAwsLambdaHandlerManager(s.apiHandler, s.settings.AwsAPIGatewayBasePath)

	return handlerMngr.SelectAdapter(s.settings.LambdaAdapter)
}

// Stop shutdown the API server.
// It waits for StartWithContext to return (servers drained) or for ctx to be done.
func (s *Server) Stop(ctx context.Context) {
	err := s.otelService.Stop(ctx)
	if err != nil {
		s.logger.LogAttrs(context.WithoutCancel(ctx), slog.LevelError, "impossible to stop otel", cmdlog.Err(err))
	}

	s.stop()

	select {
	case <-s.stopped:
	case <-ctx.Done():
		s.logger.LogAttrs(
			context.WithoutCancel(ctx), slog.LevelError, "error stopping relay proxy", cmdlog.Err(ctx.Err()))
	}
}

// isMonitoringPortConfigured checks if the monitoring port is configured.
func (s *Server) isMonitoringPortConfigured() bool {
	return s.monitoringHandler != nil && s.settings.MonitoringPort > 0
}

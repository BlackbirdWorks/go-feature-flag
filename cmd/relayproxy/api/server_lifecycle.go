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

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	cmdlog "github.com/blackbirdworks/semaphore/cmdhelpers/log"
)

// gracefulShutdownTimeout is the time given to in-flight requests to complete when the servers are stopped.
const gracefulShutdownTimeout = 5 * time.Second

// defaultReadTimeout is how long the server waits for a request to be read, its body included (a defence against
// slow clients). WithReadTimeout changes it.
const defaultReadTimeout = 30 * time.Second

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
	err := s.otelService.Init(ctx, s.config)
	if err != nil {
		s.logger.LogAttrs(context.Background(), slog.LevelError,
			"error while initializing OTel, continuing without tracing enabled",
			cmdlog.Err(err),
		)
		// we can continue because otel is not mandatory to start the server
	}
	// the metrics (Go runtime and HTTP server) are set up with the traces, on the same configuration.
	if metricsErr := s.otelService.InitMetrics(ctx, s.config); metricsErr != nil {
		s.logger.LogAttrs(context.Background(), slog.LevelError,
			"error while initializing OTel metrics, continuing without OTel metrics",
			cmdlog.Err(metricsErr),
		)
	}
	if s.installGlobalTelemetry && (err == nil || s.otelService.MeterProvider() != nil) {
		s.otelService.Install(s.logger)
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

	switch s.config.ServerMode(s.logger) {
	case config.ServerModeLambda:
		s.startAwsLambda()
	case config.ServerModeUnixSocket:
		s.startUnixSocketServer(ctx)
	default:
		s.startAsHTTPServer(ctx)
	}
}

// startUnixSocketServer launch the API server as a unix socket.
func (s *Server) startUnixSocketServer(ctx context.Context) {
	socketPath := s.config.UnixSocketPath()

	// Clean up the old socket file if it exists (important for graceful restarts)
	if _, err := os.Stat(socketPath); err == nil {
		if removeErr := os.Remove(socketPath); removeErr != nil {
			s.fatal("Could not remove old socket file", slog.String("path", socketPath), cmdlog.Err(removeErr))
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
		s.fatal("Error creating Unix listener", cmdlog.Err(err))
	}

	defer func() {
		// the graceful shutdown already closes the listener
		if closeErr := listener.Close(); closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
			s.logger.LogAttrs(
				context.Background(),
				slog.LevelError,
				"error closing unix socket listener",
				cmdlog.Err(closeErr),
			)
		}
	}()

	s.logger.LogAttrs(context.Background(), slog.LevelInfo,
		"Starting semaphore relay proxy as unix socket...",
		slog.String("socket", socketPath),
		slog.String("version", s.config.Version))

	err = s.serve(ctx, s.apiHandler, listener, false, "error shutting down api server")
	if err != nil {
		s.fatal("Error starting relay proxy as unix socket", cmdlog.Err(err))
	}
}

// startAsHTTPServer launch the API server.
func (s *Server) startAsHTTPServer(ctx context.Context) {
	if s.isMonitoringPortConfigured() {
		var wg sync.WaitGroup
		wg.Go(func() { s.startMonitoringServer(ctx) })
		defer wg.Wait()
	}

	address := fmt.Sprintf("%s:%d", s.config.ServerHost(), s.config.ServerPort(s.logger))
	s.logger.LogAttrs(context.Background(), slog.LevelInfo,
		"Starting semaphore relay proxy ...",
		slog.String("address", address),
		slog.String("version", s.config.Version))

	// serve blocks until ctx is cancelled and the graceful shutdown has drained the connections.
	listener, err := s.listenOn(ctx, address, s.listener)
	if err == nil {
		err = s.serve(ctx, s.apiHandler, listener, s.config.Server.EnableH2C, "error shutting down api server")
	}
	if err != nil {
		s.fatal("Error starting relay proxy", cmdlog.Err(err))
	}
}

// listenOn returns the listener the server was given, or else listens on the address.
func (*Server) listenOn(ctx context.Context, address string, given net.Listener) (net.Listener, error) {
	if given != nil {
		return given, nil
	}

	return (&net.ListenConfig{}).Listen(ctx, "tcp", address)
}

// serve serves the handler on the listener until ctx is done, then it gracefully shuts the server down: the
// connections get gracefulShutdownTimeout to complete. With h2c, HTTP/2 over cleartext (prior knowledge) is served
// next to HTTP/1.1; without it the server keeps net/http's default (HTTP/1.1 only).
func (s *Server) serve(ctx context.Context, h http.Handler, ln net.Listener, h2c bool, shutdownMsg string) error {
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
		shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), gracefulShutdownTimeout)
		defer cancelShutdown()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			s.logger.LogAttrs(context.Background(), slog.LevelError, shutdownMsg, cmdlog.Err(err))
		}
	})
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	return nil
}

func (s *Server) startMonitoringServer(ctx context.Context) {
	addressMonitoring := fmt.Sprintf("%s:%d", s.config.ServerHost(), s.config.EffectiveMonitoringPort(s.logger))
	s.logger.LogAttrs(context.Background(), slog.LevelInfo,
		"Starting monitoring",
		slog.String("address", addressMonitoring))
	listener, err := s.listenOn(ctx, addressMonitoring, s.monitoringListener)
	if err == nil {
		err = s.serve(ctx, s.monitoringHandler, listener, false, "error stopping monitoring")
	}
	if err != nil {
		s.fatal("Error starting monitoring", cmdlog.Err(err))
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
	handlerMngr := newAwsLambdaHandlerManager(s.apiHandler, s.config.EffectiveAwsAPIGatewayBasePath(s.logger))

	return handlerMngr.SelectAdapter(s.config.LambdaAdapter(s.logger))
}

// Stop shutdown the API server.
// It waits for StartWithContext to return (servers drained) or for ctx to be done.
func (s *Server) Stop(ctx context.Context) {
	err := s.otelService.Stop(ctx)
	if err != nil {
		s.logger.LogAttrs(context.Background(), slog.LevelError, "impossible to stop otel", cmdlog.Err(err))
	}

	s.stop()
	select {
	case <-s.stopped:
	case <-ctx.Done():
		s.logger.LogAttrs(context.Background(), slog.LevelError, "error stopping relay proxy", cmdlog.Err(ctx.Err()))
	}
}

// isMonitoringPortConfigured checks if the monitoring port is configured.
func (s *Server) isMonitoringPortConfigured() bool {
	return s.monitoringHandler != nil && s.config.EffectiveMonitoringPort(s.logger) > 0
}

// fatal logs the message at the fatal level and exits the process.
func (s *Server) fatal(msg string, attrs ...slog.Attr) {
	s.logger.LogAttrs(context.Background(), cmdlog.LevelFatal, msg, attrs...)
	s.exit()
}

package api

import (
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api/opentelemetry"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
)

type AuthMiddlewareType = string

const (
	UserAuth  AuthMiddlewareType = "USER"
	AdminAuth AuthMiddlewareType = "ADMIN"
)

// New is used to create a new instance of the API server.
func New(config *config.Config,
	services service.Services,
	logger *slog.Logger,
	opts ...Option,
) Server {
	stopRequested := make(chan struct{})
	s := Server{
		config:        config,
		services:      services,
		logger:        logger,
		exit:          func() { os.Exit(1) },
		otelService:   opentelemetry.NewOtelService(),
		stopRequested: stopRequested,
		stop:          sync.OnceFunc(func() { close(stopRequested) }),
		stopped:       make(chan struct{}),
		readTimeout:   defaultReadTimeout,
	}
	for _, opt := range opts {
		opt(&s)
	}
	s.initRoutes()

	return s
}

// Option customizes the Server that New creates.
type Option func(*Server)

// WithListener makes the HTTP server serve on an already bound listener instead of binding the configured
// address. A caller that needs a free port keeps it bound until the server uses it, instead of closing it
// and hoping that nothing else takes the port before the server binds it. The server owns the listener: it
// closes it when it stops, or when it is stopped before it started.
func WithListener(l net.Listener) Option {
	return func(s *Server) { s.listener = l }
}

// WithMonitoringListener is WithListener for the monitoring server: it serves on the given bound listener
// instead of binding the configured monitoring port. The monitoring port must still be configured (it
// decides whether a monitoring server exists). The server owns the listener.
func WithMonitoringListener(l net.Listener) Option {
	return func(s *Server) { s.monitoringListener = l }
}

// WithExit sets what the server does after it logged a fatal error (a failed bind, ...). The default exits the
// process with the status 1, a test replaces it to end the goroutine of the server only.
func WithExit(exit func()) Option {
	return func(s *Server) { s.exit = exit }
}

// WithGlobalTelemetry makes the server install its OpenTelemetry tracer provider, error handler and
// propagator as the process-wide ones when it starts. Only the startup wiring (main) asks for it: the
// OpenTelemetry API has no other way to give a provider to the instrumented libraries.
func WithGlobalTelemetry() Option {
	return func(s *Server) { s.installGlobalTelemetry = true }
}

// WithReadTimeout sets how long a request may take to be read, its body included: the servers close the connection
// of a client that is slower. The default is 30 seconds, the uploads (data collector, flag configuration) are bound
// by the same duration.
func WithReadTimeout(d time.Duration) Option {
	return func(s *Server) { s.readTimeout = d }
}

// WithTracing sets the tracer provider and the propagator of the spans of the requests. Without it the server
// uses the global ones of OpenTelemetry (the ones the telemetry installed at startup).
func WithTracing(provider trace.TracerProvider, propagator propagation.TextMapPropagator) Option {
	return func(s *Server) {
		s.tracerProvider = provider
		s.propagator = propagator
	}
}

// Server is the struct that represents the API server.
type Server struct {
	otelService            opentelemetry.OtelService
	tracerProvider         trace.TracerProvider
	listener               net.Listener
	monitoringListener     net.Listener
	propagator             propagation.TextMapPropagator
	apiHandler             http.Handler
	monitoringHandler      http.Handler
	logger                 *slog.Logger
	stopRequested          chan struct{}
	stop                   func()
	stopped                chan struct{}
	exit                   func()
	config                 *config.Config
	services               service.Services
	readTimeout            time.Duration
	installGlobalTelemetry bool
}

// Handler returns the handler that serves the API listener.
func (s *Server) Handler() http.Handler { return s.apiHandler }

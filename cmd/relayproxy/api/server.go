package api

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api/opentelemetry"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	cmdlog "github.com/blackbirdworks/semaphore/cmdhelpers/log"
)

// AuthMiddlewareType is the kind of authentication middleware of the API.
type AuthMiddlewareType = string

const (
	// UserAuth is the authentication middleware that checks user API keys.
	UserAuth AuthMiddlewareType = "USER"
	// AdminAuth is the authentication middleware that checks the admin API key.
	AdminAuth AuthMiddlewareType = "ADMIN"
)

// New is used to create a new instance of the API server.
func New(cfg *config.Config,
	services service.Services,
	logger *slog.Logger,
	opts ...Option,
) Server {
	stopRequested := make(chan struct{})

	srv := Server{
		config:        cfg,
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
		opt(&srv)
	}
	// the metrics (Go runtime and HTTP server) are set up before the routes: the middleware gets the meter provider
	// of the service when it is built.
	err := srv.otelService.InitMetrics(context.Background(), cfg)
	if err != nil {
		logger.LogAttrs(context.Background(), slog.LevelError,
			"error while initializing OTel metrics, continuing without OTel metrics", cmdlog.Err(err))
	}

	srv.initRoutes()

	return srv
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
// by the same duration. A duration of zero or less means no bound at all, for the servers and for the uploads: it is
// for an embedder that bounds the reads elsewhere (a proxy in front, a test), the relay proxy itself never sets it.
func WithReadTimeout(d time.Duration) Option {
	return func(s *Server) { s.readTimeout = d }
}

// WithMeterProvider sets the meter provider of the HTTP server metrics of the requests (the metrics that the tracing
// middleware reports). Without it the server uses the one of its OpenTelemetry service.
func WithMeterProvider(provider metric.MeterProvider) Option {
	return func(s *Server) { s.meterProvider = provider }
}

// WithOTelMetricReader makes the OpenTelemetry metrics of the service go to the reader instead of the exporter that
// the OTEL_* environment variables select. It is for the tests, which read the metrics themselves.
func WithOTelMetricReader(reader sdkmetric.Reader) Option {
	return func(s *Server) { s.otelService = opentelemetry.NewOtelService(opentelemetry.WithMetricReader(reader)) }
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
	meterProvider          metric.MeterProvider
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

package api

import (
	"net"
	"net/http"
	"sync"

	echootel "github.com/labstack/echo-otel/v5"
	echoprometheus "github.com/labstack/echo-prometheus"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"

	custommiddleware "github.com/blackbirdworks/semaphore/cmd/relayproxy/api/middleware"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api/opentelemetry"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/handler/manifest"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/handler/ofrep"
	controller "github.com/blackbirdworks/semaphore/cmd/relayproxy/handler/semaphore"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/helper"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	helpermiddleware "github.com/blackbirdworks/semaphore/cmdhelpers/api/middleware"
	"github.com/blackbirdworks/semaphore/cmdhelpers/configfile"
)

type AuthMiddlewareType = string

const (
	UserAuth  AuthMiddlewareType = "USER"
	AdminAuth AuthMiddlewareType = "ADMIN"
)

// New is used to create a new instance of the API server.
func New(config *config.Config,
	services service.Services,
	zapLog *zap.Logger,
	opts ...Option,
) Server {
	stopRequested := make(chan struct{})
	s := Server{
		config:        config,
		services:      services,
		zapLog:        zapLog,
		otelService:   opentelemetry.NewOtelService(),
		stopRequested: stopRequested,
		stop:          sync.OnceFunc(func() { close(stopRequested) }),
		stopped:       make(chan struct{}),
	}
	for _, opt := range opts {
		opt(&s)
	}
	s.apiEcho = echo.New()
	s.initRoutes()

	return s
}

// Option customizes the Server that New creates.
type Option func(*Server)

// WithListener makes the HTTP server serve on an already bound listener instead of binding the configured
// address. A caller that needs a free port keeps it bound until the server uses it, instead of closing it
// and hoping that nothing else takes the port before the server binds it.
func WithListener(l net.Listener) Option {
	return func(s *Server) { s.listener = l }
}

// WithGlobalTelemetry makes the server install its OpenTelemetry tracer provider, error handler and
// propagator as the process-wide ones when it starts. Only the startup wiring (main) asks for it: the
// OpenTelemetry API has no other way to give a provider to the instrumented libraries.
func WithGlobalTelemetry() Option {
	return func(s *Server) { s.installGlobalTelemetry = true }
}

// Server is the struct that represents the API server.
type Server struct {
	config         *config.Config
	apiEcho        *echo.Echo
	monitoringEcho *echo.Echo
	services       service.Services
	zapLog         *zap.Logger
	otelService    opentelemetry.OtelService
	// installGlobalTelemetry is set by WithGlobalTelemetry.
	installGlobalTelemetry bool
	// listener is set by WithListener, the server then serves on it instead of listening on the configured address.
	listener net.Listener

	// stopRequested is closed (once, by stop) when Stop is called, to gracefully shut down
	// the servers started by StartWithContext.
	stopRequested chan struct{}
	stop          func()
	// stopped is closed when StartWithContext returns.
	stopped chan struct{}
}

// initRoutes initialize the API endpoints that contain business logic and specificity for the relay proxy.
func (s *Server) initRoutes() {
	s.apiEcho.HTTPErrorHandler = echo.DefaultHTTPErrorHandler(s.config.IsDebugEnabled())
	s.apiEcho.Use(echootel.NewMiddleware(configfile.OtelTracerName))
	s.apiEcho.Use(helpermiddleware.ZapLogger(s.zapLog, s.config.IsDebugEnabled()))
	s.apiEcho.Use(middleware.BodyDumpWithConfig(middleware.BodyDumpConfig{
		Skipper: bodyDumpSkipper(s.zapLog),
		Handler: bodyDumpHandler(s.zapLog),
	}))
	if s.services.Metrics != (metric.Metrics{}) {
		s.apiEcho.Use(echoprometheus.NewMiddlewareWithConfig(echoprometheus.MiddlewareConfig{
			Subsystem:  metric.SubSystem,
			Registerer: s.services.Metrics.Registry,
			HistogramOptsFunc: func(opts prometheus.HistogramOpts) prometheus.HistogramOpts {
				// Enable native histograms for all middleware histograms
				// This provides higher-fidelity latency distributions while preserving
				// classic histogram buckets for backward compatibility.
				opts.NativeHistogramBucketFactor = 1.1
				opts.NativeHistogramMaxBucketNumber = 160

				return opts
			},
		}))
	}
	s.apiEcho.Use(corsMiddleware())

	s.apiEcho.Use(custommiddleware.VersionHeader(custommiddleware.VersionHeaderConfig{
		Skipper: func(_ *echo.Context) bool {
			return s.config.DisableVersionHeader
		},
		RelayProxyConfig: s.config,
	}))

	s.apiEcho.Use(middleware.Recover())

	// Init controllers
	cAllFlags := controller.NewAllFlags(s.services.FlagsetManager, s.services.Metrics)
	cFlagEval := controller.NewFlagEval(s.services.FlagsetManager, s.services.Metrics)
	cFlagEvalOFREP := ofrep.NewOFREPEvaluate(
		s.services.FlagsetManager,
		s.services.Metrics,
		s.config.OfrepEventStream,
		ofrep.WithLegacyMetadataOnly(s.config.DisableSemaphoreMetadata),
	)
	cManifest := manifest.NewManifest(s.services.FlagsetManager, s.services.Metrics, s.zapLog)
	cEvalDataCollector := controller.NewCollectEvalData(
		s.services.FlagsetManager,
		s.services.Metrics,
		s.zapLog,
	)
	cRetrieverRefresh := controller.NewForceFlagsRefresh(
		s.services.FlagsetManager,
		s.services.Metrics,
	)
	cValidateFlags := controller.NewValidateFlags(
		s.config.Version, s.services.Metrics, s.config.Endpoints.ValidateFlags)
	cFlagChangeAPI := controller.NewAPIFlagChange(
		s.services.FlagsetManager,
		s.services.Metrics,
	)
	cFlagConfiguration := controller.NewAPIFlagConfiguration(
		s.services.FlagsetManager,
		s.services.Metrics,
	)

	// Init routes
	userAuth := s.getAuthMiddleware(UserAuth)
	adminAuth := s.getAuthMiddleware(AdminAuth)
	s.addRelayRoutes(cAllFlags, cFlagEval, cEvalDataCollector, cFlagChangeAPI, cFlagConfiguration, userAuth)
	s.addOFREPRoutes(cFlagEvalOFREP, userAuth)
	s.addStreamRoutes()
	s.addMonitoringRoutes()
	s.addAdminRoutes(cRetrieverRefresh, cValidateFlags, adminAuth)
	s.addManifestRoutes(cManifest, userAuth)
}

// corsMiddleware allows any origin and answers every preflight with a fixed list of methods.
// AllowMethods is set explicitly because, when left empty, echo v5 answers the preflight with
// the methods registered on the route, which would change the CORS contract of the relay proxy.
func corsMiddleware() echo.MiddlewareFunc {
	return middleware.CORSWithConfig(middleware.CORSConfig{
		AllowOrigins: []string{"*"},
		AllowMethods: []string{
			http.MethodGet, http.MethodHead, http.MethodPut,
			http.MethodPatch, http.MethodPost, http.MethodDelete,
		},
	})
}

func (s *Server) getAuthMiddleware(middlewareType AuthMiddlewareType) echo.MiddlewareFunc {
	switch middlewareType {
	case AdminAuth:
		return custommiddleware.KeyAuthExtended(custommiddleware.KeyAuthExtendedConfig{
			Validator: func(_ *echo.Context, key string, _ middleware.ExtractorSource) (bool, error) {
				return s.config.APIKeysAdminExists(key), nil
			},
			ErrorHandler: custommiddleware.AuthMiddlewareErrHandler,
		})
	default:
		return custommiddleware.KeyAuthExtended(custommiddleware.KeyAuthExtendedConfig{
			Validator: func(c *echo.Context, key string, _ middleware.ExtractorSource) (bool, error) {
				manager := s.services.FlagsetManager
				if manager == nil || manager.IsDefaultFlagSet() {
					return s.config.APIKeyExists(key), nil
				}
				// In flag set mode a key is accepted if it routes to a flag set: the routing is the single
				// table, and the flag set resolved here is the one the handler uses.
				// An admin key is accepted and then refused by the handler, as documented.
				return helper.AuthenticateFlagSet(c, manager, key) || s.config.APIKeysAdminExists(key), nil
			},
			ErrorHandler: custommiddleware.AuthMiddlewareErrHandler,
			Skipper: func(_ *echo.Context) bool {
				return !s.config.IsAuthenticationEnabled()
			},
		})
	}
}

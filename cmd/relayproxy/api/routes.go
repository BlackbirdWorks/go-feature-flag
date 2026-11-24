package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"strconv"

	"github.com/danielgtaylor/huma/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api/middleware"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/handler/manifest"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/handler/ofrep"
	controller "github.com/blackbirdworks/semaphore/cmd/relayproxy/handler/semaphore"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/helper"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/model"
)

// routeSet is the router of one listener and the huma API that describes and serves its operations.
type routeSet struct {
	api huma.API
	mux *http.ServeMux
}

func (s *Server) newRouteSet(docs bool) *routeSet {
	mux := http.NewServeMux()
	// what no route serves, a request with a method that the route does not serve included, is a 404.
	mux.Handle("/", helper.Handle(func(http.ResponseWriter, *http.Request) error { return helper.ErrNotFound },
		s.config.IsDebugEnabled()))

	return &routeSet{mux: mux, api: newHumaAPI(mux, s.config.IsDebugEnabled(), docs)}
}

// register serves a typed operation with huma, with the middleware of the operation (they run in order).
func register[I, O any](
	rs *routeSet, op huma.Operation, handler func(context.Context, *I) (*O, error),
	humaMiddleware ...func(huma.Context, func(huma.Context)),
) {
	op.Middlewares = append(op.Middlewares, humaMiddleware...)
	huma.Register(rs.api, op, handler)
	// huma documents a validation error (422) and a default error on its own: the relay proxy answers neither
	// (its handlers decode and refuse what they get), the document lists the statuses of the operation only.
	item := rs.api.OpenAPI().Paths[op.Path]
	for _, o := range []*huma.Operation{item.Get, item.Post} {
		if o != nil && o.OperationID == op.OperationID {
			delete(o.Responses, "default")
			delete(o.Responses, "422")
		}
	}
}

// raw serves an operation without huma: the handler is a [http.Handler] (the hot path of the evaluation, the
// streams, the endpoint that reads its body itself), the operation is only documented, from the same types.
// pattern is the route of the mux, the parameters of the path are read with [http.Request.PathValue].
func (rs *routeSet) raw(op *huma.Operation, pattern string, h http.Handler, mws ...middleware.Func) {
	rs.api.OpenAPI().AddOperation(op)
	rs.mux.Handle(op.Method+" "+pattern, middleware.Chain(h, mws...))
}

func (s *Server) userKeyAuth() middleware.KeyAuth {
	return middleware.NewKeyAuth(middleware.KeyAuthConfig{
		Skipper: func() bool { return !s.config.IsAuthenticationEnabled() },
		Validator: func(ctx context.Context, key string) (context.Context, bool) {
			manager := s.services.FlagsetManager
			if manager == nil || manager.IsDefaultFlagSet() {
				return ctx, s.config.APIKeyExists(key)
			}
			// In flag set mode a key is accepted if it routes to a flag set: the routing is the single
			// table, and the flag set resolved here is the one the handler uses.
			// An admin key is accepted and then refused by the handler, as documented.
			if out, ok := helper.AuthenticateFlagSet(ctx, manager, key); ok {
				return out, true
			}

			return ctx, s.config.APIKeysAdminExists(key)
		},
	})
}

func (s *Server) adminKeyAuth() middleware.KeyAuth {
	return middleware.NewKeyAuth(middleware.KeyAuthConfig{
		Validator: func(ctx context.Context, key string) (context.Context, bool) {
			return ctx, s.config.APIKeysAdminExists(key)
		},
	})
}

// initRoutes builds the routers of the listeners, with the endpoints that contain business logic and specificity
// for the relay proxy, and their middleware.
func (s *Server) initRoutes() {
	rs := s.newRouteSet(s.config.IsSwaggerEnabled())
	if s.config.IsSwaggerEnabled() {
		doc := rs.api.OpenAPI()
		doc.Info.Version = s.config.Version
		doc.Servers = []*huma.Server{{URL: "http://" + net.JoinHostPort(
			s.config.SwaggerHost(), strconv.Itoa(s.config.ServerPort(s.logger)))}}
	}
	var monitoring *routeSet
	if s.config.EffectiveMonitoringPort(s.logger) != 0 {
		monitoring = s.newRouteSet(false)
		s.addMonitoringRoutes(monitoring)
	} else {
		s.addMonitoringRoutes(rs)
	}
	s.addAPIRoutes(rs)

	s.apiHandler = s.apiChain(rs.mux)
	if monitoring != nil {
		s.monitoringHandler = s.monitoringChain(monitoring.mux)
	}
}

// addAPIRoutes registers the endpoints of the evaluation, the streams, the admin and the manifest.
func (s *Server) addAPIRoutes(rs *routeSet) {
	userAuth := s.userKeyAuth()
	adminAuth := s.adminKeyAuth()
	api := rs.api
	debug := s.config.IsDebugEnabled()

	// the controllers
	cAllFlags := controller.NewAllFlags(s.services.FlagsetManager, s.services.Metrics)
	cFlagEval := controller.NewFlagEval(s.services.FlagsetManager, s.services.Metrics)
	cFlagEvalOFREP := ofrep.NewOFREPEvaluate(
		s.services.FlagsetManager,
		s.services.Metrics,
		s.config.OfrepEventStream,
		ofrep.WithLegacyMetadataOnly(s.config.DisableSemaphoreMetadata),
	)
	cManifest := manifest.NewManifest(s.services.FlagsetManager, s.services.Metrics, s.logger)
	cEvalDataCollector := controller.NewCollectEvalData(s.services.FlagsetManager, s.services.Metrics, s.logger)
	cRetrieverRefresh := controller.NewForceFlagsRefresh(s.services.FlagsetManager, s.services.Metrics)
	cValidateFlags := controller.NewValidateFlags(
		s.config.Version,
		s.services.Metrics,
		s.config.Endpoints.ValidateFlags,
	)
	cFlagChangeAPI := controller.NewAPIFlagChange(s.services.FlagsetManager, s.services.Metrics)
	cFlagConfiguration := controller.NewAPIFlagConfiguration(s.services.FlagsetManager, s.services.Metrics)
	cWsFlagChange := controller.NewWsFlagChange(s.services.WebsocketService, s.services.FlagsetManager, s.logger)
	cSSEFlagChange := controller.NewSSEFlagChange(s.services.SSEService, s.services.FlagsetManager, s.logger)

	// /v1: the evaluation API
	op := operationAllFlags(api)
	rs.raw(op, "/v1/allflags", helper.Handle(cAllFlags.Handler, debug), userAuth.HTTP())
	op = operationFlagEval(api)
	rs.raw(op, "/v1/feature/{flagKey}/eval", helper.Handle(cFlagEval.Handler, debug), userAuth.HTTP())

	collector := operationCollector()
	controller.BodyOpts(&collector)
	register(rs, collector, cEvalDataCollector.Handle, userAuth.Huma(api))
	setRequestBody(api, collector.OperationID, jsonBody(api,
		"List of flag evaluation that be passed to the data exporter", true, model.CollectEvalDataRequest{}))

	register(rs, operationFlagChange(), cFlagChangeAPI.Handle, userAuth.Huma(api), middleware.ETagHuma())

	configuration := operationFlagConfiguration()
	controller.BodyOpts(&configuration)
	register(rs, configuration, cFlagConfiguration.Handle, userAuth.Huma(api), middleware.ETagHuma())
	setRequestBody(api, configuration.OperationID, jsonBody(api,
		"List of flags to get the configuration from.", false, controller.FlagConfigurationRequest{}))

	// /ofrep/v1: OpenFeature Remote Evaluation Protocol
	rs.raw(operationOFREPBulk(api), "/ofrep/v1/evaluate/flags", helper.Handle(cFlagEvalOFREP.BulkEvaluate, debug),
		middleware.ETag(), userAuth.HTTP())
	rs.raw(operationOFREPEval(api), "/ofrep/v1/evaluate/flags/{flagKey}",
		helper.Handle(cFlagEvalOFREP.Evaluate, debug), userAuth.HTTP())

	// /stream/v1: the streams of flag changes, and the legacy websocket alias
	authorize := middleware.StreamAuthorizer(s.config, s.services.FlagsetManager, debug)
	rs.raw(
		operationWebsocket(api, false),
		"/stream/v1/ws/flag/change",
		helper.Handle(cWsFlagChange.Handler, debug),
		authorize,
	)
	rs.raw(operationSSE(api), "/stream/v1/sse/flag/change", helper.Handle(cSSEFlagChange.Handler, debug), authorize)
	rs.raw(operationWebsocket(api, true), "/ws/v1/flag/change", helper.Handle(cWsFlagChange.LegacyHandler, debug),
		authorize, middleware.DeprecatedAlias("/stream/v1/ws/flag/change"))

	// /admin/v1
	register(rs, operationRefresh(), cRetrieverRefresh.Handle, adminAuth.Huma(api))
	rs.raw(operationValidate(api), "/admin/v1/flags/validate", helper.Handle(cValidateFlags.Handler, debug),
		adminAuth.HTTP())

	// /openfeature/v0
	register(rs, operationManifest(), cManifest.GetManifest, userAuth.Huma(api))
}

// addMonitoringRoutes registers the monitoring endpoints: the health, the info, the metrics, pprof and, when it is
// enabled, the Swagger UI. They are served by the monitoring listener when there is one, else by the API listener.
func (s *Server) addMonitoringRoutes(rs *routeSet) {
	api := rs.api

	register(rs, operationHealth(), controller.NewHealth(s.services.MonitoringService).Handle)
	register(rs, operationInfo(), controller.NewInfo(s.services.MonitoringService).Handle)

	api.OpenAPI().AddOperation(operationMetrics())
	if s.services.Metrics != (metric.Metrics{}) {
		gatherer := s.services.Metrics.Gatherer(s.config.ExposeLegacyMetrics)
		h := promhttp.HandlerFor(gatherer, promhttp.HandlerOpts{DisableCompression: true})
		if r, ok := gatherer.(prometheus.Registerer); ok {
			h = promhttp.InstrumentMetricHandler(r, h)
		}
		rs.mux.Handle("GET /metrics", h)
	}

	api.OpenAPI().AddOperation(operationPprof())
	if s.config.IsDebugEnabled() || s.config.EnablePprof {
		const prefix = "/debug/pprof"
		for name, h := range map[string]http.HandlerFunc{
			"/{$}":          pprof.Index,
			"/cmdline":      pprof.Cmdline,
			"/profile":      pprof.Profile,
			"/symbol":       pprof.Symbol,
			"/trace":        pprof.Trace,
			"/allocs":       pprof.Handler("allocs").ServeHTTP,
			"/block":        pprof.Handler("block").ServeHTTP,
			"/goroutine":    pprof.Handler("goroutine").ServeHTTP,
			"/heap":         pprof.Handler("heap").ServeHTTP,
			"/mutex":        pprof.Handler("mutex").ServeHTTP,
			"/threadcreate": pprof.Handler("threadcreate").ServeHTTP,
		} {
			rs.mux.Handle("GET "+prefix+name, h)
		}
		rs.mux.Handle("POST "+prefix+"/symbol", http.HandlerFunc(pprof.Symbol))
	}
}

// OpenAPI returns the OpenAPI document of the relay proxy, generated from the operations it registers.
func OpenAPI() *huma.OpenAPI {
	// a server with all the optional endpoints, on one listener: its router holds every operation.
	s := &Server{config: documentedConfig(), logger: slog.New(slog.DiscardHandler)}
	rs := s.newRouteSet(true)
	s.addAPIRoutes(rs)
	s.addMonitoringRoutes(rs)

	return rs.api.OpenAPI()
}

// OpenAPIJSON returns the OpenAPI document as it is committed in the golden file: indented JSON, with a newline at
// the end.
func OpenAPIJSON() ([]byte, error) {
	doc, err := json.MarshalIndent(OpenAPI(), "", "  ")
	if err != nil {
		return nil, fmt.Errorf("impossible to encode the OpenAPI document: %w", err)
	}

	return append(doc, '\n'), nil
}

// documentedConfig is the configuration of the server that describes every endpoint.
func documentedConfig() *config.Config {
	return &config.Config{EnablePprof: true, Swagger: config.Swagger{Enabled: true}}
}

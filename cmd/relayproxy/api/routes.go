package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/http/pprof"

	"github.com/danielgtaylor/huma/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api/middleware"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/handler/manifest"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/handler/ofrep"
	controller "github.com/blackbirdworks/semaphore/cmd/relayproxy/handler/semaphore"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/helper"
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

	if docs {
		// the documented entry point of the Swagger UI.
		mux.Handle("GET /swagger/{$}", http.RedirectHandler("/swagger/index.html", http.StatusFound))
	}

	return &routeSet{mux: mux, api: newHumaAPI(mux, s.config.IsDebugEnabled(), docs)}
}

// mergeOpenAPI adds the operations and the schemas of src to dst.
func mergeOpenAPI(dst, src *huma.OpenAPI) {
	maps.Copy(dst.Paths, src.Paths)

	known := dst.Components.Schemas.Map()
	for name, schema := range src.Components.Schemas.Map() {
		if _, ok := known[name]; !ok {
			known[name] = schema
		}
	}
}

// primeDocs makes huma render the documents now. huma renders them on the first request and keeps them in a
// variable that the handlers share without a lock: rendering them before the listener starts, once everything is
// registered, leaves the concurrent requests nothing to write.
func primeDocs(mux http.Handler) {
	for _, path := range []string{
		"/swagger/openapi.json", "/swagger/openapi-3.0.json", "/swagger/openapi.yaml", "/swagger/openapi-3.0.yaml",
	} {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, path, http.NoBody)
		if err != nil {
			continue
		}

		mux.ServeHTTP(discardWriter{header: http.Header{}}, req)
	}
}

// discardWriter drops the response.
type discardWriter struct{ header http.Header }

func (w discardWriter) Header() http.Header { return w.header }

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

func (discardWriter) WriteHeader(int) {}

// register serves a typed operation with huma, with the middleware of the operation (they run in order).
func register[I, O any](
	routes *routeSet, operationConf *huma.Operation, handler func(context.Context, *I) (*O, error),
	humaMiddleware ...func(huma.Context, func(huma.Context)),
) {
	// the operation of the caller is not changed: huma registers a copy.
	operation := *operationConf
	operation.Middlewares = append(operation.Middlewares, humaMiddleware...)
	// the error responses are documented with the body the relay proxy sends, not huma's RFC 9457 model.
	errorStatuses := operation.Errors
	operation.Errors = nil
	huma.Register(routes.api, operation, handler)
	// huma documents a validation error (422) and a default error on its own: the relay proxy answers neither
	// (its handlers decode and refuse what they get), the document lists the statuses of the operation only.
	item := routes.api.OpenAPI().Paths[operation.Path]
	for _, opItem := range []*huma.Operation{item.Get, item.Post} {
		if opItem != nil && opItem.OperationID == operation.OperationID {
			delete(opItem.Responses, "default")
			delete(opItem.Responses, "422")

			for _, status := range errorStatuses {
				opItem.Responses[statusKey(status)] = errorResponse(routes.api, http.StatusText(status))
			}
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
		Skipper: nil,
		Validator: func(ctx context.Context, key string) (context.Context, bool) {
			return ctx, s.config.APIKeysAdminExists(key)
		},
	})
}

// initRoutes builds the routers of the listeners, with the endpoints that contain business logic and specificity
// for the relay proxy, and their middleware.
func (s *Server) initRoutes() {
	routes := s.newRouteSet(s.config.IsSwaggerEnabled())

	swagger := s.config.IsSwaggerEnabled()
	if swagger {
		// no server is advertised: the requests of the Swagger UI go to the origin of the page, whatever scheme
		// and host an ingress serves it with.
		routes.api.OpenAPI().Info.Version = s.config.Version
	}

	var monitoring *routeSet
	if s.config.EffectiveMonitoringPort(s.logger) != 0 {
		monitoring = s.newRouteSet(false)
		s.addMonitoringRoutes(monitoring)
	} else {
		s.addMonitoringRoutes(routes)
	}

	s.addAPIRoutes(routes)

	if swagger {
		if monitoring != nil {
			// one document describes the whole API, whether the monitoring endpoints are served by this listener
			// or by the dedicated one.
			mergeOpenAPI(routes.api.OpenAPI(), monitoring.api.OpenAPI())
		}

		primeDocs(routes.mux)
	}

	s.apiHandler = s.apiChain(routes.mux)
	if monitoring != nil {
		s.monitoringHandler = s.monitoringChain(monitoring.mux)
	}
}

// addAPIRoutes registers the endpoints of the evaluation, the streams, the admin and the manifest.
func (s *Server) addAPIRoutes(routes *routeSet) {
	userAuth := s.userKeyAuth()
	adminAuth := s.adminKeyAuth()
	api := routes.api
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
	cManifest := manifest.NewManifest(s.services.FlagsetManager, s.services.Metrics)
	cEvalDataCollector := controller.NewCollectEvalData(s.services.FlagsetManager, s.services.Metrics)
	cRetrieverRefresh := controller.NewForceFlagsRefresh(s.services.FlagsetManager, s.services.Metrics)
	cValidateFlags := controller.NewValidateFlags(
		s.config.Version,
		s.services.Metrics,
		s.config.Endpoints.ValidateFlags,
	)
	cFlagChangeAPI := controller.NewAPIFlagChange(s.services.FlagsetManager, s.services.Metrics)
	cFlagConfiguration := controller.NewAPIFlagConfiguration(s.services.FlagsetManager, s.services.Metrics)
	cWsFlagChange := controller.NewWsFlagChange(s.services.WebsocketService, s.services.FlagsetManager)
	cSSEFlagChange := controller.NewSSEFlagChange(s.services.SSEService, s.services.FlagsetManager)

	// /v1: the evaluation API
	op := operationAllFlags(api)
	routes.raw(op, "/v1/allflags", helper.Handle(cAllFlags.Handler, debug), userAuth.HTTP())
	op = operationFlagEval(api)
	routes.raw(op, "/v1/feature/{flagKey}/eval", helper.Handle(cFlagEval.Handler, debug), userAuth.HTTP())

	collector := operationCollector()
	controller.BodyOpts(collector, s.readTimeout)
	register(routes, collector, cEvalDataCollector.Handle, userAuth.Huma())
	setRequestBody(api, collector.OperationID, jsonBody(api,
		"List of flag evaluation that be passed to the data exporter", true, model.CollectEvalDataRequest{}))

	register(routes, operationFlagChange(), cFlagChangeAPI.Handle, userAuth.Huma(), middleware.ETagHuma())

	configuration := operationFlagConfiguration()
	controller.BodyOpts(configuration, s.readTimeout)
	register(routes, configuration, cFlagConfiguration.Handle, userAuth.Huma(), middleware.ETagHuma())
	setRequestBody(api, configuration.OperationID, jsonBody(api,
		"List of flags to get the configuration from.", false, controller.FlagConfigurationRequest{}))

	// /ofrep/v1: OpenFeature Remote Evaluation Protocol
	routes.raw(operationOFREPBulk(api), "/ofrep/v1/evaluate/flags", helper.Handle(cFlagEvalOFREP.BulkEvaluate, debug),
		middleware.ETag(), userAuth.HTTP())
	routes.raw(operationOFREPEval(api), "/ofrep/v1/evaluate/flags/{flagKey}",
		helper.Handle(cFlagEvalOFREP.Evaluate, debug), userAuth.HTTP())

	// /stream/v1: the streams of flag changes, and the legacy websocket alias
	authorize := middleware.StreamAuthorizer(s.config, s.services.FlagsetManager, debug)
	routes.raw(
		operationWebsocket(api, false),
		"/stream/v1/ws/flag/change",
		helper.Handle(cWsFlagChange.Handler, debug),
		authorize,
	)
	routes.raw(operationSSE(api), "/stream/v1/sse/flag/change", helper.Handle(cSSEFlagChange.Handler, debug), authorize)
	routes.raw(operationWebsocket(api, true), "/ws/v1/flag/change", helper.Handle(cWsFlagChange.LegacyHandler, debug),
		authorize, middleware.DeprecatedAlias("/stream/v1/ws/flag/change"))

	// /admin/v1
	register(routes, operationRefresh(), cRetrieverRefresh.Handle, adminAuth.Huma())
	routes.raw(operationValidate(api), "/admin/v1/flags/validate", helper.Handle(cValidateFlags.Handler, debug),
		adminAuth.HTTP())

	// /openfeature/v0
	register(routes, operationManifest(), cManifest.GetManifest, userAuth.Huma())
}

// addMonitoringRoutes registers the monitoring endpoints: the health, the info, the metrics, pprof and, when it is
// enabled, the Swagger UI. They are served by the monitoring listener when there is one, else by the API listener.
func (s *Server) addMonitoringRoutes(routes *routeSet) {
	api := routes.api

	register(routes, operationHealth(), controller.NewHealth(s.services.MonitoringService).Handle)
	register(routes, operationInfo(), controller.NewInfo(s.services.MonitoringService).Handle)

	api.OpenAPI().AddOperation(operationMetrics())

	if s.services.Metrics.Enabled() {
		gatherer := s.services.Metrics.Gatherer(s.config.ExposeLegacyMetrics)

		h := promhttp.HandlerFor(gatherer, promhttp.HandlerOpts{DisableCompression: true})
		if r, ok := gatherer.(prometheus.Registerer); ok {
			h = promhttp.InstrumentMetricHandler(r, h)
		}

		routes.mux.Handle("GET /metrics", h)
	}

	api.OpenAPI().AddOperation(operationPprof())

	if s.config.IsDebugEnabled() || s.config.EnablePprof {
		const prefix = "/debug/pprof"
		for name, handler := range map[string]http.HandlerFunc{
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
			routes.mux.Handle("GET "+prefix+name, handler)
		}

		routes.mux.Handle("POST "+prefix+"/symbol", http.HandlerFunc(pprof.Symbol))
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
	return &config.Config{EnablePprof: true, Swagger: config.Swagger{Enabled: true, Host: ""}}
}

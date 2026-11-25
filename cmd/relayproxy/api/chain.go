package api

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/blackbirdworks/huma-middleware/middleware/accesslog"
	"github.com/blackbirdworks/huma-middleware/middleware/cleanpath"
	"github.com/blackbirdworks/huma-middleware/middleware/logging"
	"github.com/blackbirdworks/huma-middleware/middleware/recovery"
	"github.com/blackbirdworks/huma-middleware/middleware/tracing"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api/middleware"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api/opentelemetry"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/helper"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	reqlog "github.com/blackbirdworks/semaphore/cmdhelpers/api/middleware"
	"github.com/blackbirdworks/semaphore/cmdhelpers/cmdlog"
)

// This file is the one place where the middleware that wrap a whole listener are assembled. They are plain
// http.Handler wrappers around the mux, the first one is the outermost:
//
//  1. tracing (OpenTelemetry server span, the library does it with otelhttp), only when OpenTelemetry is
//     configured: without an exporter there is nothing to send and the middleware is not installed. It is outside
//     the logging, so the request logger and the access log know the trace and span IDs;
//  2. the request logger: the logger of the request, in the context of every request, with the request ID;
//  3. the access log, with the status that was sent (a recovered panic is a 500 record, at the error level). It is
//     outside cleanpath: it logs the request as the client sent it, an unclean path included;
//  4. cleanpath: the path is cleaned (double slash, dot segments) before the mux routes it;
//  5. the version header, so that every answer carries it, the 500 of a recovered panic included;
//  6. the request body dump (debug level) and the HTTP metrics (API listener only);
//  7. CORS (answers the preflight requests, so these are logged, traced and counted too);
//  8. the panic recovery, innermost: it logs the panic with the error level and answers 500, which the middleware
//     above (access log, spans, metrics) then see like any other 500.
//
// The order matters: the mux sets the route of the request ([http.Request.Pattern]) on the request it receives.
// The middleware that copy the request (tracing, request logger, cleanpath) give the pattern back to the request they
// received, so the ones that read the route after the request was served (the span name, the access log) see it. The
// metrics label reads it as well and sits next to the mux.
//
// The dedicated monitoring listener has the same chain without the tracing, the body dump and the metrics, as
// before. The authentication and the ETag are per route: they are middleware of the operations.

func (s *Server) tracingOptions() []tracing.Option {
	// the meter provider is the one of the service: the HTTP server metrics of the requests go where the runtime
	// metrics go. Nothing is process-wide: the provider is given where it is used.
	var opts []tracing.Option
	if meter := s.meterProvider; meter != nil {
		opts = append(opts, tracing.WithMeterProvider(meter))
	} else if provider := s.otelService.MeterProvider(); provider != nil {
		opts = append(opts, tracing.WithMeterProvider(provider))
	}

	if s.tracerProvider != nil {
		opts = append(opts, tracing.WithTracerProvider(s.tracerProvider))
	}

	if s.propagator != nil {
		opts = append(opts, tracing.WithPropagators(s.propagator))
	}

	return opts
}

// requestLogger installs the logger of the request: every request context carries a logger derived from the logger
// of the service, with the request ID. The logger is built when a handler asks for it, a request that does not log
// pays for no logger.
func (s *Server) requestLogger() middleware.Func {
	return func(next http.Handler) http.Handler {
		return logging.Handler(next, logging.WithLogger(s.logger))
	}
}

func (s *Server) accessLog() middleware.Func {
	return accesslog.Handler(
		accesslog.WithLogger(s.logger),
		accesslog.WithRecordFunc(reqlog.AccessLog()),
		accesslog.WithRequestSkip(reqlog.SkipRequest(s.config.IsDebugEnabled())),
	)
}

// tracingEnabled reports whether the requests are traced: there is an exporter configured (or a tracer provider was
// given to the server). Otherwise the tracing middleware is not installed, it would cost on every request for
// spans nobody receives.
func (s *Server) tracingEnabled() bool {
	return s.tracerProvider != nil || opentelemetry.Enabled(s.config)
}

func (s *Server) tracing() middleware.Func {
	return func(next http.Handler) http.Handler { return tracing.Handler(next, s.tracingOptions()...) }
}

// apiChain wraps the mux of the API listener.
func (s *Server) apiChain(mux http.Handler) http.Handler {
	var chain []middleware.Func
	if s.tracingEnabled() {
		chain = append(chain, s.tracing())
	}

	chain = append(chain,
		s.requestLogger(),
		s.accessLog(),
		cleanpath.Handler(),
		middleware.VersionHeader(s.config),
		middleware.BodyDump(bodyDumpSkipper(s.logger), bodyDumpHandler()),
	)
	if s.services.Metrics.Enabled() {
		prom, err := middleware.Prometheus(middleware.PrometheusConfig{
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
		})
		if err != nil {
			s.logger.LogAttrs(context.Background(), slog.LevelError, "impossible to register the HTTP metrics",
				cmdlog.Err(err))
		} else {
			chain = append(chain, prom)
		}
	}

	return middleware.Chain(
		mux,
		append(chain, middleware.CORS(), s.recovery())...)
}

// monitoringChain wraps the mux of the dedicated monitoring listener.
func (s *Server) monitoringChain(mux http.Handler) http.Handler {
	return middleware.Chain(mux,
		s.requestLogger(),
		s.accessLog(),
		cleanpath.Handler(),
		middleware.VersionHeader(s.config),
		middleware.CORS(),
		s.recovery(),
	)
}

// recovery turns a panic of a handler into a 500 with the error body of the relay proxy; the library logs it with
// the error level through the logger of the server and marks the span.
func (s *Server) recovery() middleware.Func {
	debug := s.config.IsDebugEnabled()

	return recovery.Handler(
		recovery.WithRequestHandler(func(w http.ResponseWriter, r *http.Request, recovered any) {
			err := &recovery.PanicError{Value: recovered}
			reqlog.RecordError(r.Context(), err)
			helper.WriteError(w, r, err, debug)
		}),
	)
}

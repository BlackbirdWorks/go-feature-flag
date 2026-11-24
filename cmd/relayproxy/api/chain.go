package api

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/blackbirdworks/huma-middleware/middleware/cleanpath"
	"github.com/blackbirdworks/huma-middleware/middleware/logging"
	"github.com/blackbirdworks/huma-middleware/middleware/tracing"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api/middleware"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api/opentelemetry"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	reqlog "github.com/blackbirdworks/semaphore/cmdhelpers/api/middleware"
	cmdlog "github.com/blackbirdworks/semaphore/cmdhelpers/log"
)

// This file is the one place where the middleware that wrap a whole listener are assembled. They are plain
// http.Handler wrappers around the mux, the first one is the outermost:
//
//  1. cleanpath: the path is cleaned (double slash, dot segments) before the mux routes it;
//  2. the version header, so that every answer carries it, the 500 of a recovered panic included;
//  3. the access log, with the status that was sent (a recovered panic is a 500 record, at the error level);
//  4. tracing (OpenTelemetry server span, the library does it with otelhttp), only when OpenTelemetry is
//     configured: without an exporter there is nothing to send and the middleware is not installed;
//  5. the request body dump (debug level) and the HTTP metrics (API listener only);
//  6. CORS (answers the preflight requests, so these are logged, traced and counted too);
//  7. the panic recovery, innermost: it logs the panic with the error level and answers 500, which the middleware
//     above (access log, spans, metrics) then see like any other 500.
//
// The order matters: the mux sets the route of the request ([http.Request.Pattern]) on the request it receives.
// The tracing and the access log hand a copy of the request to the next handler, so the ones that read the route
// after the request was served (the span name, the metrics label) must be the closest to the mux, and nothing between
// them and the mux copies the request.
//
// The dedicated monitoring listener has the same chain without the tracing, the body dump and the metrics, as
// before. The authentication and the ETag are per route: they are middleware of the operations.

func (s *Server) tracingOptions() []tracing.Option {
	var opts []tracing.Option
	if s.tracerProvider != nil {
		opts = append(opts, tracing.WithTracerProvider(s.tracerProvider))
	}
	if s.propagator != nil {
		opts = append(opts, tracing.WithPropagators(s.propagator))
	}

	return opts
}

func (s *Server) loggingOptions() []logging.Option {
	return []logging.Option{
		logging.WithLogger(s.logger),
		// nothing reads the logger of the request: the handlers log with the logger they were given.
		logging.WithoutRequestAttrs(),
		logging.WithAccessLogFunc(reqlog.AccessLog()),
		logging.WithRequestSkip(reqlog.SkipRequest(s.config.IsDebugEnabled())),
	}
}

func (s *Server) accessLog() middleware.Func {
	return func(next http.Handler) http.Handler { return logging.Handler(next, s.loggingOptions()...) }
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
	chain := []middleware.Func{
		cleanpath.Handler(),
		middleware.VersionHeader(s.config),
		s.accessLog(),
	}
	if s.tracingEnabled() {
		chain = append(chain, s.tracing())
	}
	chain = append(chain, middleware.BodyDump(bodyDumpSkipper(s.logger), bodyDumpHandler(s.logger)))
	if s.services.Metrics != (metric.Metrics{}) {
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
		append(chain, middleware.CORS(), middleware.Recover(s.logger, s.config.IsDebugEnabled()))...)
}

// monitoringChain wraps the mux of the dedicated monitoring listener.
func (s *Server) monitoringChain(mux http.Handler) http.Handler {
	return middleware.Chain(mux,
		cleanpath.Handler(),
		middleware.VersionHeader(s.config),
		s.accessLog(),
		middleware.CORS(),
		middleware.Recover(s.logger, s.config.IsDebugEnabled()),
	)
}

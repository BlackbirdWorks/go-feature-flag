package middleware

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// sizeBuckets are the buckets of the size histograms: 1 KiB to 10 MiB.
func sizeBuckets() []float64 {
	const (
		kb = 1 << 10
		mb = 1 << 20
	)

	return []float64{1 * kb, 2 * kb, 5 * kb, 10 * kb, 100 * kb, 500 * kb, 1 * mb, 2.5 * mb, 5 * mb, 10 * mb}
}

// PrometheusConfig is the configuration of the HTTP metrics.
type PrometheusConfig struct {
	// Registerer receives the metrics.
	Registerer prometheus.Registerer
	// HistogramOptsFunc changes the options of every histogram.
	HistogramOptsFunc func(opts prometheus.HistogramOpts) prometheus.HistogramOpts
	// Subsystem is the subsystem of the names of the metrics.
	Subsystem string
}

// RoutePattern returns the route that served the request, in the form of the labels of the metrics: the path
// template with its parameters written ":name" ("/v1/feature/:flagKey/eval"), or the path of the request when no
// route matched (the mux answers it with the pattern "/").
func RoutePattern(r *http.Request) string {
	pattern := r.Pattern
	if _, route, found := strings.Cut(pattern, " "); found {
		pattern = route
	}
	if pattern == "" || pattern == "/" {
		return r.URL.Path
	}

	return routeLabel(pattern)
}

// routeLabel writes the parameters of a mux pattern ":name", a rest-of-path parameter "*", and drops the "{$}" that
// anchors a path ending with a slash.
func routeLabel(pattern string) string {
	var b strings.Builder
	for {
		start := strings.IndexByte(pattern, '{')
		if start < 0 {
			break
		}
		end := strings.IndexByte(pattern[start:], '}')
		if end < 0 {
			break
		}
		b.WriteString(pattern[:start])
		name := pattern[start+1 : start+end]
		switch {
		case name == "$":
		case strings.HasSuffix(name, "..."):
			b.WriteString("*")
		default:
			b.WriteString(":" + name)
		}
		pattern = pattern[start+end+1:]
	}
	b.WriteString(pattern)

	return b.String()
}

// Prometheus measures the requests: how many, how long, how big the requests and the responses are, by status
// code, method, host and route (the route, not the path, so the number of series stays bounded).
func Prometheus(cfg PrometheusConfig) (Func, error) {
	if cfg.HistogramOptsFunc == nil {
		cfg.HistogramOptsFunc = func(opts prometheus.HistogramOpts) prometheus.HistogramOpts { return opts }
	}
	labels := []string{"code", "method", "host", "url"}

	requestCount := prometheus.NewCounterVec(prometheus.CounterOpts{
		Subsystem: cfg.Subsystem,
		Name:      "requests_total",
		Help:      "How many HTTP requests processed, partitioned by status code and HTTP method.",
	}, labels)
	requestDuration := prometheus.NewHistogramVec(cfg.HistogramOptsFunc(prometheus.HistogramOpts{
		Subsystem: cfg.Subsystem,
		Name:      "request_duration_seconds",
		Help:      "The HTTP request latencies in seconds.",
		Buckets:   prometheus.DefBuckets,
	}), labels)
	responseSize := prometheus.NewHistogramVec(cfg.HistogramOptsFunc(prometheus.HistogramOpts{
		Subsystem: cfg.Subsystem,
		Name:      "response_size_bytes",
		Help:      "The HTTP response sizes in bytes.",
		Buckets:   sizeBuckets(),
	}), labels)
	requestSize := prometheus.NewHistogramVec(cfg.HistogramOptsFunc(prometheus.HistogramOpts{
		Subsystem: cfg.Subsystem,
		Name:      "request_size_bytes",
		Help:      "The HTTP request sizes in bytes.",
		Buckets:   sizeBuckets(),
	}), labels)
	for _, c := range []prometheus.Collector{requestCount, requestDuration, responseSize, requestSize} {
		if err := cfg.Registerer.Register(c); err != nil {
			return nil, err //nolint:wrapcheck // the error of the registry says what is wrong
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reqSize := approximateRequestSize(r)
			rec := newRecorder(w)
			start := time.Now()
			next.ServeHTTP(rec, r)
			elapsed := time.Since(start).Seconds()

			values := [...]string{
				strconv.Itoa(rec.Status()),
				r.Method,
				r.Host,
				strings.ToValidUTF8(RoutePattern(r), "�"),
			}
			if obs, err := requestDuration.GetMetricWithLabelValues(values[:]...); err == nil {
				obs.Observe(elapsed)
			}
			if obs, err := requestCount.GetMetricWithLabelValues(values[:]...); err == nil {
				obs.Inc()
			}
			if obs, err := requestSize.GetMetricWithLabelValues(values[:]...); err == nil {
				obs.Observe(float64(reqSize))
			}
			if obs, err := responseSize.GetMetricWithLabelValues(values[:]...); err == nil {
				obs.Observe(float64(rec.Size()))
			}
		})
	}, nil
}

func approximateRequestSize(r *http.Request) int {
	s := 0
	if r.URL != nil {
		s = len(r.URL.Path)
	}
	s += len(r.Method) + len(r.Proto)
	for name, values := range r.Header {
		s += len(name)
		for _, value := range values {
			s += len(value)
		}
	}
	s += len(r.Host)
	if r.ContentLength != -1 {
		s += int(r.ContentLength)
	}

	return s
}

// recorder remembers the status and the number of body bytes that were sent.
type recorder struct {
	http.ResponseWriter

	status int
	size   int64
}

func newRecorder(w http.ResponseWriter) *recorder { return &recorder{ResponseWriter: w} }

// Status returns the status sent to the client: 200 when the handler wrote a body (or nothing) without a status.
func (r *recorder) Status() int {
	if r.status == 0 {
		return http.StatusOK
	}

	return r.status
}

// Size returns the number of body bytes sent.
func (r *recorder) Size() int64 { return r.size }

// WriteHeader records the status, informational statuses are not final.
func (r *recorder) WriteHeader(code int) {
	if r.status == 0 && (code >= http.StatusOK || code == http.StatusSwitchingProtocols) {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

// Write sends the body and counts it.
func (r *recorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.size += int64(n)

	return n, err
}

// Unwrap lets [http.ResponseController] reach the underlying writer (flush, hijack, deadlines).
func (r *recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// Flush sends the buffered data to the client.
func (r *recorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

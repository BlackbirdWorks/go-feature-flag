package middleware_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api/middleware"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/helper"
)

// newOK returns a handler that answers a JSON document.
func newOK() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
}

func serve(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

// key is the context key under which the tests of the authentication read the key that was validated.
type key struct{}

// document is the output of the operation the huma entry points of the middleware are tested with.
type document struct {
	Body struct {
		Name string `json:"name"`
	}
	Status int
}

// humaHandler serves one operation, GET /doc, behind a huma API, with the middleware: the same behaviours as the
// [http.Handler] form are checked through it.
func humaHandler(
	handler func(context.Context, *struct{}) (*document, error), mws ...func(huma.Context, func(huma.Context)),
) http.Handler {
	mux := http.NewServeMux()
	cfg := huma.DefaultConfig("test", "1.0.0")
	cfg.OpenAPIPath, cfg.DocsPath, cfg.SchemasPath = "", "", ""
	cfg.CreateHooks = nil
	huma.Register(humago.New(mux, cfg), huma.Operation{
		OperationID: "doc", Method: http.MethodGet, Path: "/doc", Middlewares: mws,
	}, handler)

	return mux
}

func okDocument(context.Context, *struct{}) (*document, error) {
	out := &document{Status: http.StatusOK}
	out.Body.Name = "x"

	return out, nil
}

// TestKeyAuth checks the authentication through its two entry points: a [http.Handler] and a huma operation.
func TestKeyAuth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		headers    map[string]string
		name       string
		entry      string
		wantBody   string
		wantKey    string
		wantStatus int
		skip       bool
	}{
		{name: "bearer", entry: "http", headers: map[string]string{"Authorization": "Bearer good"},
			wantStatus: http.StatusOK, wantKey: "good"},
		{name: "bearer lower case", entry: "http", headers: map[string]string{"Authorization": "bearer good"},
			wantStatus: http.StatusOK, wantKey: "good"},
		{name: "x-api-key", entry: "http", headers: map[string]string{"X-API-Key": "good"},
			wantStatus: http.StatusOK, wantKey: "good"},
		{name: "x-api-key wins over bearer", entry: "http",
			headers:    map[string]string{"X-API-Key": "bad", "Authorization": "Bearer good"},
			wantStatus: http.StatusUnauthorized, wantBody: `{"message":"Unauthorized"}` + "\n"},
		{name: "no header", entry: "http", wantStatus: http.StatusUnauthorized,
			wantBody: `{"message":"missing value in request header"}` + "\n"},
		{name: "not a bearer", entry: "http", headers: map[string]string{"Authorization": "Basic good"},
			wantStatus: http.StatusUnauthorized, wantBody: `{"message":"invalid value in request header"}` + "\n"},
		{name: "empty bearer", entry: "http", headers: map[string]string{"Authorization": "Bearer "},
			wantStatus: http.StatusUnauthorized, wantBody: `{"message":"invalid value in request header"}` + "\n"},
		{
			name:       "wrong key",
			entry:      "http",
			headers:    map[string]string{"Authorization": "Bearer bad"},
			wantStatus: http.StatusUnauthorized,
			wantBody:   `{"message":"authenticating the API key: code=401, message=invalid key"}` + "\n",
		},
		{name: "skipped", entry: "http", skip: true, wantStatus: http.StatusOK},
		{name: "huma bearer", entry: "huma", headers: map[string]string{"Authorization": "Bearer good"},
			wantStatus: http.StatusOK, wantKey: "good"},
		{name: "huma x-api-key", entry: "huma", headers: map[string]string{"X-API-Key": "good"},
			wantStatus: http.StatusOK, wantKey: "good"},
		{name: "huma no key", entry: "huma", wantStatus: http.StatusUnauthorized,
			wantBody: `{"message":"missing value in request header"}` + "\n"},
		{name: "huma wrong key", entry: "huma", headers: map[string]string{"X-API-Key": "bad"},
			wantStatus: http.StatusUnauthorized, wantBody: `{"message":"Unauthorized"}` + "\n"},
		{name: "huma skipped", entry: "huma", skip: true, wantStatus: http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			auth := middleware.NewKeyAuth(middleware.KeyAuthConfig{
				Validator: func(ctx context.Context, k string) (context.Context, bool) {
					return context.WithValue(ctx, key{}, k), k == "good"
				},
				Skipper: func() bool { return tt.skip },
			})

			var (
				gotKey  string
				handler http.Handler
			)
			if tt.entry == "huma" {
				handler = humaHandler(func(ctx context.Context, in *struct{}) (*document, error) {
					gotKey, _ = ctx.Value(key{}).(string)

					return okDocument(ctx, in)
				}, auth.Huma())
			} else {
				handler = auth.HTTP()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					gotKey, _ = r.Context().Value(key{}).(string)
					newOK().ServeHTTP(w, r)
				}))
			}

			req := httptest.NewRequest(http.MethodGet, "/doc", nil)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}

			rec := serve(handler, req)
			assert.Equal(t, tt.wantStatus, rec.Code)
			assert.Equal(t, tt.wantKey, gotKey)

			if tt.wantBody != "" {
				assert.Equal(t, tt.wantBody, rec.Body.String())
			}
		})
	}
}

func TestVersionHeader(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		conf    *config.Config
		wantNew string
	}{
		{name: "enabled", conf: &config.Config{Version: "1.0.0"}, wantNew: "1.0.0"},
		{name: "disabled", conf: &config.Config{Version: "1.0.0", DisableVersionHeader: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := serve(
				middleware.VersionHeader(tt.conf)(newOK()),
				httptest.NewRequest(http.MethodGet, "/", nil),
			)
			assert.Equal(t, tt.wantNew, rec.Header().Get(middleware.VersionHeaderName))
			assert.Equal(t, tt.wantNew, rec.Header().Get(middleware.LegacyVersionHeaderName))
		})
	}
}

func TestCORS(t *testing.T) {
	t.Parallel()

	tests := []struct {
		headers     map[string]string
		name        string
		method      string
		wantOrigin  string
		wantMethods string
		wantHeaders string
		wantStatus  int
	}{
		{name: "no origin", method: http.MethodGet, wantStatus: http.StatusOK},
		{name: "simple request", method: http.MethodGet, headers: map[string]string{"Origin": "https://a.example"},
			wantStatus: http.StatusOK, wantOrigin: "*"},
		{name: "options without request method", method: http.MethodOptions,
			headers: map[string]string{"Origin": "https://a.example"}, wantStatus: http.StatusOK, wantOrigin: "*"},
		{name: "preflight", method: http.MethodOptions, headers: map[string]string{
			"Origin": "https://a.example", "Access-Control-Request-Method": "POST",
			"Access-Control-Request-Headers": "x-api-key",
		}, wantStatus: http.StatusNoContent, wantOrigin: "*", wantMethods: "GET,HEAD,PUT,PATCH,POST,DELETE",
			wantHeaders: "x-api-key"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(tt.method, "/", nil)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}

			rec := serve(middleware.CORS()(newOK()), req)
			assert.Equal(t, tt.wantStatus, rec.Code)
			assert.Equal(t, tt.wantOrigin, rec.Header().Get("Access-Control-Allow-Origin"))
			assert.Equal(t, tt.wantMethods, rec.Header().Get("Access-Control-Allow-Methods"))
			assert.Equal(t, tt.wantHeaders, rec.Header().Get("Access-Control-Allow-Headers"))
			assert.Contains(t, rec.Header().Values("Vary"), "Origin")
		})
	}
}

// TestETag checks the ETag through its two entry points: a [http.Handler] and a huma operation. Every row first asks
// for the document to know its ETag, then sends the request of the row.
func TestETag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		entry      string
		condition  string
		wantBody   string
		wantStatus int
		failing    bool
		wantETag   bool
	}{
		{name: "no condition", entry: "http", wantStatus: http.StatusOK, wantBody: `{"ok":true}`, wantETag: true},
		{name: "matching", entry: "http", condition: "etag", wantStatus: http.StatusNotModified, wantETag: true},
		{name: "matching weak", entry: "http", condition: "weak", wantStatus: http.StatusNotModified, wantETag: true},
		{
			name:       "other",
			entry:      "http",
			condition:  "other",
			wantStatus: http.StatusOK,
			wantBody:   `{"ok":true}`,
			wantETag:   true,
		},
		{name: "error untouched", entry: "http", failing: true, wantStatus: http.StatusTeapot, wantBody: "tea"},
		{
			name:       "huma no condition",
			entry:      "huma",
			wantStatus: http.StatusOK,
			wantBody:   `{"name":"x"}` + "\n",
			wantETag:   true,
		},
		{name: "huma matching", entry: "huma", condition: "etag", wantStatus: http.StatusNotModified, wantETag: true},
		{
			name:       "huma other",
			entry:      "huma",
			condition:  "other",
			wantStatus: http.StatusOK,
			wantBody:   `{"name":"x"}` + "\n",
			wantETag:   true,
		},
		{
			name:       "huma error untouched",
			entry:      "huma",
			failing:    true,
			wantStatus: http.StatusTeapot,
			wantBody:   `{"Message":"tea","Code":418}` + "\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var handler http.Handler

			switch {
			case tt.entry == "huma" && tt.failing:
				handler = humaHandler(func(context.Context, *struct{}) (*document, error) {
					return nil, helper.NewHTTPError(http.StatusTeapot, "tea")
				}, middleware.ETagHuma())
			case tt.entry == "huma":
				handler = humaHandler(okDocument, middleware.ETagHuma())
			case tt.failing:
				handler = middleware.ETag()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusTeapot)
					_, _ = w.Write([]byte("tea"))
				}))
			default:
				handler = middleware.ETag()(newOK())
			}

			// the ETag of the document
			etag := serve(handler, httptest.NewRequest(http.MethodGet, "/doc", nil)).Header().Get("ETag")
			if tt.wantETag {
				require.NotEmpty(t, etag)
			}

			req := httptest.NewRequest(http.MethodGet, "/doc", nil)

			switch tt.condition {
			case "etag":
				req.Header.Set("If-None-Match", etag)
			case "weak":
				req.Header.Set("If-None-Match", "W/"+etag)
			case "other":
				req.Header.Set("If-None-Match", `"x"`)
			}

			rec := serve(handler, req)
			assert.Equal(t, tt.wantStatus, rec.Code)
			assert.Equal(t, tt.wantBody, rec.Body.String())

			if tt.wantETag {
				assert.Equal(t, etag, rec.Header().Get("ETag"))
			} else {
				assert.Empty(t, rec.Header().Get("ETag"))
			}
		})
	}
}

// TestBodyDump checks the body that is given to the dump and the body the handler reads.
func TestBodyDump(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		path       string
		wantDump   string
		wantRead   string
		wantCalled bool
	}{
		{name: "dumped", path: "/x", wantDump: "hello", wantRead: "hello", wantCalled: true},
		{name: "skipped", path: "/skip", wantRead: "hello"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var (
				dumped, read string
				called       bool
			)

			handler := middleware.BodyDump(
				func(r *http.Request) bool { return r.URL.Path == "/skip" },
				func(_ *http.Request, body []byte) { called, dumped = true, string(body) },
			)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				read = string(b)
			}))

			serve(handler, httptest.NewRequest(http.MethodPost, tt.path, strings.NewReader("hello")))
			assert.Equal(t, tt.wantCalled, called, "the dump is called only for a request that is not skipped")
			assert.Equal(t, tt.wantDump, dumped)
			assert.Equal(t, tt.wantRead, read)
		})
	}
}

// TestDeprecatedAlias checks the headers that point to the replacement of a deprecated endpoint.
func TestDeprecatedAlias(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		replacement string
		wantLink    string
	}{
		{name: "new endpoint", replacement: "/new", wantLink: `</new>; rel="successor-version"`},
		{name: "other endpoint", replacement: "/stream/v1/x", wantLink: `</stream/v1/x>; rel="successor-version"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := serve(
				middleware.DeprecatedAlias(tt.replacement)(newOK()),
				httptest.NewRequest(http.MethodGet, "/old", nil),
			)
			assert.Equal(t, "true", rec.Header().Get("Deprecation"))
			assert.Equal(t, tt.wantLink, rec.Header().Get("Link"))
		})
	}
}

// TestChain checks that the first middleware of a chain is the outermost.
func TestChain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		want  string
		names []string
	}{
		{name: "three", names: []string{"a", "b", "c"}, want: "abc"},
		{name: "one", names: []string{"a"}, want: "a"},
		{name: "none", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var (
				order bytes.Buffer
				mws   []middleware.Func
			)
			for _, name := range tt.names {
				mws = append(mws, func(next http.Handler) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						order.WriteString(name)
						next.ServeHTTP(w, r)
					})
				})
			}

			serve(middleware.Chain(newOK(), mws...), httptest.NewRequest(http.MethodGet, "/", nil))
			assert.Equal(t, tt.want, order.String())
		})
	}
}

// TestPrometheus checks the series of the HTTP metrics, and that they are registered once.
func TestPrometheus(t *testing.T) {
	t.Parallel()

	allLabels := []string{"code=418", "host=example.com", "method=GET", "url=/items/:id"}

	tests := []struct {
		wantSeries   map[string][]string
		name         string
		preRegister  bool
		wantRegister bool
	}{
		{
			name: "series", wantRegister: true,
			wantSeries: map[string][]string{
				"sub_requests_total": allLabels, "sub_request_duration_seconds": allLabels,
				"sub_request_size_bytes": allLabels, "sub_response_size_bytes": allLabels,
			},
		},
		{name: "registered twice", preRegister: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reg := prometheus.NewRegistry()

			cfg := middleware.PrometheusConfig{Registerer: reg, Subsystem: "sub"}
			if tt.preRegister {
				_, err := middleware.Prometheus(cfg)
				require.NoError(t, err)
			}

			prom, err := middleware.Prometheus(cfg)
			if !tt.wantRegister {
				require.Error(t, err, "the metrics are registered once")

				return
			}

			require.NoError(t, err)

			serve(prom(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				r.Pattern = "GET /items/{id}"

				w.WriteHeader(http.StatusTeapot)
				_, _ = w.Write([]byte("tea"))
			})), httptest.NewRequest(http.MethodGet, "/items/42", nil))

			families, err := reg.Gather()
			require.NoError(t, err)

			got := map[string][]string{}

			for _, family := range families {
				for _, m := range family.GetMetric() {
					labels := make([]string, 0, len(m.GetLabel()))
					for _, l := range m.GetLabel() {
						labels = append(labels, l.GetName()+"="+l.GetValue())
					}

					got[family.GetName()] = labels
				}
			}

			assert.Equal(t, tt.wantSeries, got)
		})
	}
}

func TestRoutePattern(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		pattern string
		want    string
	}{
		{name: "no route matched", pattern: "/", want: "/a/b"},
		{name: "no pattern", pattern: "", want: "/a/b"},
		{name: "static route", pattern: "GET /health", want: "/health"},
		{name: "parameter", pattern: "POST /v1/feature/{flagKey}/eval", want: "/v1/feature/:flagKey/eval"},
		{name: "two parameters", pattern: "/a/{x}/b/{y}", want: "/a/:x/b/:y"},
		{name: "rest of the path", pattern: "GET /files/{path...}", want: "/files/*"},
		{name: "anchored path", pattern: "GET /debug/pprof/{$}", want: "/debug/pprof/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodGet, "/a/b", nil)
			req.Pattern = tt.pattern
			assert.Equal(t, tt.want, middleware.RoutePattern(req))
		})
	}
}

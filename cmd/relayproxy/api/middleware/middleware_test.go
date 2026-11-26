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
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/helper"
	"github.com/blackbirdworks/semaphore/internal/protocol"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

const (
	nestedPath          = "/a/b"
	bearerGood          = "Bearer good"
	doc                 = "/doc"
	etagLower           = "etag"
	good                = "good"
	httpsAExample       = "https://a.example"
	humaName            = "huma"
	messageUnauthorized = `{"message":"Unauthorized"}`
	okTrue              = `{"ok":true}`
	other               = "other"
	tea                 = "tea"
	xAPIKeyUpper        = "X-API-Key"
	xAPIKeyLower        = "x-api-key"
)

// newOK returns a handler that answers a JSON document.
func newOK() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(okTrue))
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
	cfg := huma.DefaultConfig("test", fixtures.VersionOneZeroZero)
	cfg.OpenAPIPath, cfg.DocsPath, cfg.SchemasPath = "", "", ""
	cfg.CreateHooks = nil
	huma.Register(humago.New(mux, cfg), huma.Operation{
		OperationID: "doc", Method: http.MethodGet, Path: doc, Middlewares: mws,
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
		{name: "bearer", entry: fixtures.KindHTTP, headers: map[string]string{protocol.HeaderAuthorization: bearerGood},
			wantStatus: http.StatusOK, wantKey: good},
		{
			name:       "bearer lower case",
			entry:      fixtures.KindHTTP,
			headers:    map[string]string{protocol.HeaderAuthorization: "bearer good"},
			wantStatus: http.StatusOK,
			wantKey:    good,
		},
		{name: xAPIKeyLower, entry: fixtures.KindHTTP, headers: map[string]string{xAPIKeyUpper: good},
			wantStatus: http.StatusOK, wantKey: good},
		{name: "x-api-key wins over bearer", entry: fixtures.KindHTTP,
			headers:    map[string]string{xAPIKeyUpper: "bad", protocol.HeaderAuthorization: bearerGood},
			wantStatus: http.StatusUnauthorized, wantBody: messageUnauthorized + "\n"},
		{name: "no header", entry: fixtures.KindHTTP, wantStatus: http.StatusUnauthorized,
			wantBody: `{"message":"missing value in request header"}` + "\n"},
		{
			name:       "not a bearer",
			entry:      fixtures.KindHTTP,
			headers:    map[string]string{protocol.HeaderAuthorization: "Basic good"},
			wantStatus: http.StatusUnauthorized,
			wantBody:   `{"message":"invalid value in request header"}` + "\n",
		},
		{
			name:       "empty bearer",
			entry:      fixtures.KindHTTP,
			headers:    map[string]string{protocol.HeaderAuthorization: "Bearer "},
			wantStatus: http.StatusUnauthorized,
			wantBody:   `{"message":"invalid value in request header"}` + "\n",
		},
		{
			name:       "wrong key",
			entry:      fixtures.KindHTTP,
			headers:    map[string]string{protocol.HeaderAuthorization: "Bearer bad"},
			wantStatus: http.StatusUnauthorized,
			wantBody:   `{"message":"authenticating the API key: code=401, message=invalid key"}` + "\n",
		},
		{name: "skipped", entry: fixtures.KindHTTP, skip: true, wantStatus: http.StatusOK},
		{name: "huma bearer", entry: humaName, headers: map[string]string{protocol.HeaderAuthorization: bearerGood},
			wantStatus: http.StatusOK, wantKey: good},
		{name: "huma x-api-key", entry: humaName, headers: map[string]string{xAPIKeyUpper: good},
			wantStatus: http.StatusOK, wantKey: good},
		{name: "huma no key", entry: humaName, wantStatus: http.StatusUnauthorized,
			wantBody: `{"message":"missing value in request header"}` + "\n"},
		{name: "huma wrong key", entry: humaName, headers: map[string]string{xAPIKeyUpper: "bad"},
			wantStatus: http.StatusUnauthorized, wantBody: messageUnauthorized + "\n"},
		{name: "huma skipped", entry: humaName, skip: true, wantStatus: http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			auth := middleware.NewKeyAuth(middleware.KeyAuthConfig{
				Validator: func(ctx context.Context, k string) (context.Context, bool) {
					return context.WithValue(ctx, key{}, k), k == good
				},
				Skipper: func() bool { return tt.skip },
			})

			var (
				gotKey  string
				handler http.Handler
			)
			if tt.entry == humaName {
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

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, doc, nil)
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
		name     string
		wantNew  string
		disabled bool
	}{
		{name: "enabled", wantNew: fixtures.VersionOneZeroZero},
		{name: "disabled", disabled: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := serve(
				middleware.VersionHeader(fixtures.VersionOneZeroZero, tt.disabled)(newOK()),
				httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil),
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
		{
			name:       "simple request",
			method:     http.MethodGet,
			headers:    map[string]string{protocol.HeaderOrigin: httpsAExample},
			wantStatus: http.StatusOK,
			wantOrigin: "*",
		},
		{
			name:   "options without request method",
			method: http.MethodOptions,
			headers: map[string]string{
				protocol.HeaderOrigin: httpsAExample,
			},
			wantStatus: http.StatusOK,
			wantOrigin: "*",
		},
		{name: "preflight", method: http.MethodOptions, headers: map[string]string{
			protocol.HeaderOrigin: httpsAExample, "Access-Control-Request-Method": "POST",
			"Access-Control-Request-Headers": xAPIKeyLower,
		}, wantStatus: http.StatusNoContent, wantOrigin: "*", wantMethods: "GET,HEAD,PUT,PATCH,POST,DELETE",
			wantHeaders: xAPIKeyLower},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(t.Context(), tt.method, "/", nil)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}

			rec := serve(middleware.CORS()(newOK()), req)
			assert.Equal(t, tt.wantStatus, rec.Code)
			assert.Equal(t, tt.wantOrigin, rec.Header().Get("Access-Control-Allow-Origin"))
			assert.Equal(t, tt.wantMethods, rec.Header().Get("Access-Control-Allow-Methods"))
			assert.Equal(t, tt.wantHeaders, rec.Header().Get("Access-Control-Allow-Headers"))
			assert.Contains(t, rec.Header().Values("Vary"), protocol.HeaderOrigin)
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
		{name: "no condition", entry: fixtures.KindHTTP, wantStatus: http.StatusOK, wantBody: okTrue, wantETag: true},
		{
			name:       "matching",
			entry:      fixtures.KindHTTP,
			condition:  etagLower,
			wantStatus: http.StatusNotModified,
			wantETag:   true,
		},
		{
			name:       "matching weak",
			entry:      fixtures.KindHTTP,
			condition:  "weak",
			wantStatus: http.StatusNotModified,
			wantETag:   true,
		},
		{
			name:       other,
			entry:      fixtures.KindHTTP,
			condition:  other,
			wantStatus: http.StatusOK,
			wantBody:   okTrue,
			wantETag:   true,
		},
		{
			name:       "error untouched",
			entry:      fixtures.KindHTTP,
			failing:    true,
			wantStatus: http.StatusTeapot,
			wantBody:   tea,
		},
		{
			name:       "huma no condition",
			entry:      humaName,
			wantStatus: http.StatusOK,
			wantBody:   `{"name":"x"}` + "\n",
			wantETag:   true,
		},
		{
			name:       "huma matching",
			entry:      humaName,
			condition:  etagLower,
			wantStatus: http.StatusNotModified,
			wantETag:   true,
		},
		{
			name:       "huma other",
			entry:      humaName,
			condition:  other,
			wantStatus: http.StatusOK,
			wantBody:   `{"name":"x"}` + "\n",
			wantETag:   true,
		},
		{
			name:       "huma error untouched",
			entry:      humaName,
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
			case tt.entry == humaName && tt.failing:
				handler = humaHandler(func(context.Context, *struct{}) (*document, error) {
					return nil, helper.NewHTTPError(http.StatusTeapot, tea)
				}, middleware.ETagHuma())
			case tt.entry == humaName:
				handler = humaHandler(okDocument, middleware.ETagHuma())
			case tt.failing:
				handler = middleware.ETag()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusTeapot)
					_, _ = w.Write([]byte(tea))
				}))
			default:
				handler = middleware.ETag()(newOK())
			}

			// the ETag of the document
			etag := serve(
				handler,
				httptest.NewRequestWithContext(t.Context(), http.MethodGet, doc, nil),
			).Header().
				Get(protocol.HeaderETag)
			if tt.wantETag {
				require.NotEmpty(t, etag)
			}

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, doc, nil)

			switch tt.condition {
			case etagLower:
				req.Header.Set(protocol.HeaderIfNoneMatch, etag)
			case "weak":
				req.Header.Set(protocol.HeaderIfNoneMatch, "W/"+etag)
			case other:
				req.Header.Set(protocol.HeaderIfNoneMatch, `"x"`)
			}

			rec := serve(handler, req)
			assert.Equal(t, tt.wantStatus, rec.Code)
			assert.Equal(t, tt.wantBody, rec.Body.String())

			if tt.wantETag {
				assert.Equal(t, etag, rec.Header().Get(protocol.HeaderETag))
			} else {
				assert.Empty(t, rec.Header().Get(protocol.HeaderETag))
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
		{name: "dumped", path: "/x", wantDump: fixtures.BodyHello, wantRead: fixtures.BodyHello, wantCalled: true},
		{name: "skipped", path: "/skip", wantRead: fixtures.BodyHello},
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

			serve(
				handler,
				httptest.NewRequestWithContext(
					t.Context(),
					http.MethodPost,
					tt.path,
					strings.NewReader(fixtures.BodyHello),
				),
			)
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
				httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/old", nil),
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

			serve(
				middleware.Chain(newOK(), mws...),
				httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil),
			)
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
				_, _ = w.Write([]byte(tea))
			})), httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/items/42", nil))

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
		{name: "no route matched", pattern: "/", want: nestedPath},
		{name: "no pattern", pattern: "", want: nestedPath},
		{name: "static route", pattern: "GET /health", want: "/health"},
		{name: "parameter", pattern: "POST /v1/feature/{flagKey}/eval", want: "/v1/feature/:flagKey/eval"},
		{name: "two parameters", pattern: "/a/{x}/b/{y}", want: "/a/:x/b/:y"},
		{name: "rest of the path", pattern: "GET /files/{path...}", want: "/files/*"},
		{name: "anchored path", pattern: "GET /debug/pprof/{$}", want: "/debug/pprof/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, nestedPath, nil)
			req.Pattern = tt.pattern
			assert.Equal(t, tt.want, middleware.RoutePattern(req))
		})
	}
}

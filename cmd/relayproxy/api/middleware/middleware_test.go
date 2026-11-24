package middleware_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api/middleware"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
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

func TestKeyAuth(t *testing.T) {
	t.Parallel()

	type key struct{}
	auth := middleware.NewKeyAuth(middleware.KeyAuthConfig{
		Validator: func(ctx context.Context, k string) (context.Context, bool) {
			return context.WithValue(ctx, key{}, k), k == "good"
		},
		Skipper: func() bool { return false },
	})
	tests := []struct {
		headers    map[string]string
		name       string
		wantBody   string
		wantKey    string
		wantStatus int
	}{
		{
			name:       "bearer",
			headers:    map[string]string{"Authorization": "Bearer good"},
			wantStatus: http.StatusOK,
			wantKey:    "good",
		},
		{
			name:       "bearer lower case",
			headers:    map[string]string{"Authorization": "bearer good"},
			wantStatus: http.StatusOK,
			wantKey:    "good",
		},
		{
			name:       "x-api-key",
			headers:    map[string]string{"X-API-Key": "good"},
			wantStatus: http.StatusOK,
			wantKey:    "good",
		},
		{
			name:       "x-api-key wins over bearer",
			headers:    map[string]string{"X-API-Key": "bad", "Authorization": "Bearer good"},
			wantStatus: http.StatusUnauthorized,
			wantBody:   `{"message":"Unauthorized"}` + "\n",
		},
		{name: "no header", wantStatus: http.StatusUnauthorized,
			wantBody: `{"message":"missing value in request header"}` + "\n"},
		{name: "not a bearer", headers: map[string]string{"Authorization": "Basic good"},
			wantStatus: http.StatusUnauthorized, wantBody: `{"message":"invalid value in request header"}` + "\n"},
		{name: "empty bearer", headers: map[string]string{"Authorization": "Bearer "},
			wantStatus: http.StatusUnauthorized, wantBody: `{"message":"invalid value in request header"}` + "\n"},
		{name: "wrong key", headers: map[string]string{"Authorization": "Bearer bad"},
			wantStatus: http.StatusUnauthorized, wantBody: `{"message":"code=401, message=invalid key"}` + "\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var gotKey string
			h := auth.HTTP()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotKey, _ = r.Context().Value(key{}).(string)
				newOK().ServeHTTP(w, r)
			}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			rec := serve(h, req)
			assert.Equal(t, tt.wantStatus, rec.Code)
			assert.Equal(t, tt.wantKey, gotKey)
			if tt.wantBody != "" {
				assert.Equal(t, tt.wantBody, rec.Body.String())
			}
		})
	}
}

func TestKeyAuth_Skipper(t *testing.T) {
	t.Parallel()

	auth := middleware.NewKeyAuth(middleware.KeyAuthConfig{
		Validator: func(ctx context.Context, _ string) (context.Context, bool) { return ctx, false },
		Skipper:   func() bool { return true },
	})
	rec := serve(auth.HTTP()(newOK()), httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Equal(t, http.StatusOK, rec.Code)
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

func TestETag(t *testing.T) {
	t.Parallel()

	h := middleware.ETag()(newOK())
	first := serve(h, httptest.NewRequest(http.MethodGet, "/", nil))
	etag := first.Header().Get("ETag")
	require.NotEmpty(t, etag)
	assert.Equal(t, http.StatusOK, first.Code)
	assert.JSONEq(t, `{"ok":true}`, first.Body.String())

	tests := []struct {
		name        string
		ifNoneMatch string
		wantBody    string
		wantStatus  int
	}{
		{name: "no condition", wantStatus: http.StatusOK, wantBody: `{"ok":true}`},
		{name: "matching", ifNoneMatch: etag, wantStatus: http.StatusNotModified},
		{name: "matching weak", ifNoneMatch: "W/" + etag, wantStatus: http.StatusNotModified},
		{name: "other", ifNoneMatch: `"x"`, wantStatus: http.StatusOK, wantBody: `{"ok":true}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.ifNoneMatch != "" {
				req.Header.Set("If-None-Match", tt.ifNoneMatch)
			}
			rec := serve(h, req)
			assert.Equal(t, tt.wantStatus, rec.Code)
			assert.Equal(t, etag, rec.Header().Get("ETag"))
			assert.Equal(t, tt.wantBody, rec.Body.String())
		})
	}

	t.Run("an error is sent untouched", func(t *testing.T) {
		t.Parallel()

		failing := middleware.ETag()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusTeapot)
			_, _ = w.Write([]byte("tea"))
		}))
		rec := serve(failing, httptest.NewRequest(http.MethodGet, "/", nil))
		assert.Equal(t, http.StatusTeapot, rec.Code)
		assert.Equal(t, "tea", rec.Body.String())
		assert.Empty(t, rec.Header().Get("ETag"))
	})
}

func TestBodyDump(t *testing.T) {
	t.Parallel()

	var dumped []byte
	var read []byte
	h := middleware.BodyDump(
		func(r *http.Request) bool { return r.URL.Path == "/skip" },
		func(_ *http.Request, body []byte) { dumped = body },
	)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		read, _ = io.ReadAll(r.Body)
	}))

	serve(h, httptest.NewRequest(http.MethodPost, "/x", strings.NewReader("hello")))
	assert.Equal(t, "hello", string(dumped))
	assert.Equal(t, "hello", string(read))

	dumped, read = nil, nil
	serve(h, httptest.NewRequest(http.MethodPost, "/skip", strings.NewReader("hello")))
	assert.Nil(t, dumped)
	assert.Equal(t, "hello", string(read))
}

func TestDeprecatedAlias(t *testing.T) {
	t.Parallel()

	rec := serve(
		middleware.DeprecatedAlias("/new")(newOK()),
		httptest.NewRequest(http.MethodGet, "/old", nil),
	)
	assert.Equal(t, "true", rec.Header().Get("Deprecation"))
	assert.Equal(t, `</new>; rel="successor-version"`, rec.Header().Get("Link"))
}

func TestChain(t *testing.T) {
	t.Parallel()

	var order bytes.Buffer
	mw := func(name string) middleware.Func {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order.WriteString(name)
				next.ServeHTTP(w, r)
			})
		}
	}
	serve(middleware.Chain(newOK(), mw("a"), mw("b"), mw("c")), httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Equal(t, "abc", order.String())
}

func TestPrometheus(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	prom, err := middleware.Prometheus(middleware.PrometheusConfig{Registerer: reg, Subsystem: "sub"})
	require.NoError(t, err)

	h := prom(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Pattern = "GET /items/{id}"
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("tea"))
	}))
	req := httptest.NewRequest(http.MethodGet, "/items/42", nil)
	serve(h, req)

	families, err := reg.Gather()
	require.NoError(t, err)
	got := map[string][]string{}
	for _, f := range families {
		for _, m := range f.GetMetric() {
			labels := make([]string, 0, len(m.GetLabel()))
			for _, l := range m.GetLabel() {
				labels = append(labels, l.GetName()+"="+l.GetValue())
			}
			got[f.GetName()] = labels
		}
	}
	for _, name := range []string{"sub_requests_total", "sub_request_duration_seconds", "sub_request_size_bytes",
		"sub_response_size_bytes"} {
		assert.Equal(t, []string{"code=418", "host=example.com", "method=GET", "url=/items/:id"}, got[name], name)
	}

	_, err = middleware.Prometheus(middleware.PrometheusConfig{Registerer: reg, Subsystem: "sub"})
	assert.Error(t, err, "the metrics are registered once")
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

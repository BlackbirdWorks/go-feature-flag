package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api/middleware"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/helper"
)

type document struct {
	Body struct {
		Name string `json:"name"`
	}
	Status int
}

// newHumaServer serves one operation, GET /doc, with the middleware, behind a huma API.
func newHumaServer(t *testing.T, handler func(context.Context, *struct{}) (*document, error),
	build func(huma.API) []func(huma.Context, func(huma.Context)),
) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	cfg := huma.DefaultConfig("test", "1.0.0")
	cfg.OpenAPIPath, cfg.DocsPath, cfg.SchemasPath = "", "", ""
	cfg.CreateHooks = nil
	api := humago.New(mux, cfg)
	huma.Register(api, huma.Operation{
		OperationID: "doc", Method: http.MethodGet, Path: "/doc", Middlewares: build(api),
	}, handler)

	return mux
}

func etagOnly(huma.API) []func(huma.Context, func(huma.Context)) {
	return []func(huma.Context, func(huma.Context)){middleware.ETagHuma()}
}

func okDocument(_ context.Context, _ *struct{}) (*document, error) {
	out := &document{Status: http.StatusOK}
	out.Body.Name = "x"

	return out, nil
}

func TestETagHuma(t *testing.T) {
	t.Parallel()

	h := newHumaServer(t, okDocument, etagOnly)
	first := serve(h, httptest.NewRequest(http.MethodGet, "/doc", nil))
	etag := first.Header().Get("ETag")
	require.NotEmpty(t, etag)
	assert.Equal(t, http.StatusOK, first.Code)
	assert.JSONEq(t, `{"name":"x"}`, first.Body.String())

	tests := []struct {
		name        string
		ifNoneMatch string
		wantStatus  int
		wantBody    bool
	}{
		{name: "no condition", wantStatus: http.StatusOK, wantBody: true},
		{name: "matching", ifNoneMatch: etag, wantStatus: http.StatusNotModified},
		{name: "other", ifNoneMatch: `"x"`, wantStatus: http.StatusOK, wantBody: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodGet, "/doc", nil)
			if tt.ifNoneMatch != "" {
				req.Header.Set("If-None-Match", tt.ifNoneMatch)
			}
			rec := serve(h, req)
			assert.Equal(t, tt.wantStatus, rec.Code)
			assert.Equal(t, etag, rec.Header().Get("ETag"))
			assert.Equal(t, tt.wantBody, rec.Body.Len() > 0)
		})
	}

	t.Run("an error is sent untouched", func(t *testing.T) {
		t.Parallel()

		failing := newHumaServer(t, func(context.Context, *struct{}) (*document, error) {
			return nil, helper.NewHTTPError(http.StatusTeapot, "tea")
		}, etagOnly)
		rec := serve(failing, httptest.NewRequest(http.MethodGet, "/doc", nil))
		assert.Equal(t, http.StatusTeapot, rec.Code)
		assert.Empty(t, rec.Header().Get("ETag"))
	})
}

func TestKeyAuth_Huma(t *testing.T) {
	t.Parallel()

	auth := middleware.NewKeyAuth(middleware.KeyAuthConfig{
		Validator: func(ctx context.Context, key string) (context.Context, bool) { return ctx, key == "good" },
	})
	tests := []struct {
		headers    map[string]string
		name       string
		wantBody   string
		wantStatus int
	}{
		{name: "bearer", headers: map[string]string{"Authorization": "Bearer good"}, wantStatus: http.StatusOK},
		{name: "x-api-key", headers: map[string]string{"X-API-Key": "good"}, wantStatus: http.StatusOK},
		{name: "no key", wantStatus: http.StatusUnauthorized,
			wantBody: `{"message":"missing value in request header"}` + "\n"},
		{name: "wrong key", headers: map[string]string{"X-API-Key": "bad"}, wantStatus: http.StatusUnauthorized,
			wantBody: `{"message":"Unauthorized"}` + "\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			api := newHumaServer(t, okDocument, func(huma.API) []func(huma.Context, func(huma.Context)) {
				return []func(huma.Context, func(huma.Context)){auth.Huma()}
			})
			req := httptest.NewRequest(http.MethodGet, "/doc", nil)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			rec := serve(api, req)
			assert.Equal(t, tt.wantStatus, rec.Code)
			if tt.wantBody != "" {
				assert.Equal(t, tt.wantBody, rec.Body.String())
			}
		})
	}
}

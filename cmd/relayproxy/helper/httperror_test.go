package helper_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/helper"
)

var errBoom = errors.New("boom")

func TestHTTPError_Error(t *testing.T) {
	t.Parallel()

	tests := []struct {
		err  *helper.HTTPError
		name string
		want string
	}{
		{name: "message", err: helper.NewHTTPError(http.StatusNotFound, "nf"), want: "code=404, message=nf"},
		{name: "no message", err: helper.NewHTTPError(http.StatusNotFound, ""), want: "code=404, message=Not Found"},
		{name: "wrapped", err: helper.WrapHTTPError(http.StatusBadRequest, errBoom),
			want: "code=400, message=Bad Request, err=boom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, tt.err.Error())
		})
	}
}

func TestWriteError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		err         error
		name        string
		method      string
		wantBody    string
		wantStatus  int
		exposeError bool
	}{
		{name: "http error", err: helper.NewHTTPError(http.StatusTeapot, "tea"), method: http.MethodGet,
			wantStatus: http.StatusTeapot, wantBody: `{"message":"tea"}` + "\n"},
		{
			name:       "wrapped error hidden",
			err:        helper.WrapHTTPError(http.StatusBadRequest, errBoom),
			method:     http.MethodGet,
			wantStatus: http.StatusBadRequest,
			wantBody:   `{"message":"Bad Request"}` + "\n",
		},
		{name: "wrapped error exposed", err: helper.WrapHTTPError(http.StatusBadRequest, errBoom),
			method: http.MethodGet, exposeError: true, wantStatus: http.StatusBadRequest,
			wantBody: `{"message":"Bad Request","error":"boom"}` + "\n"},
		{name: "plain error", err: errBoom, method: http.MethodGet, wantStatus: http.StatusInternalServerError,
			wantBody: `{"message":"Internal Server Error"}` + "\n"},
		{name: "plain error exposed", err: errBoom, method: http.MethodGet, exposeError: true,
			wantStatus: http.StatusInternalServerError,
			wantBody:   `{"message":"Internal Server Error","error":"boom"}` + "\n"},
		{name: "status error", err: helper.ErrUnauthorized, method: http.MethodGet, wantStatus: http.StatusUnauthorized,
			wantBody: `{"message":"Unauthorized"}` + "\n"},
		{name: "head has no body", err: helper.ErrNotFound, method: http.MethodHead, wantStatus: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			rec.Header().Set("Content-Length", "12")
			helper.WriteError(rec, httptest.NewRequestWithContext(t.Context(), tt.method, "/", nil), tt.err, tt.exposeError)
			assert.Equal(t, tt.wantStatus, rec.Code)
			assert.Equal(t, tt.wantBody, rec.Body.String())
			assert.Empty(t, rec.Header().Get("Content-Length"))
		})
	}
}

func TestHandle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		handler    func(http.ResponseWriter, *http.Request) error
		name       string
		wantBody   string
		wantStatus int
	}{
		{
			name:       "error",
			handler:    func(http.ResponseWriter, *http.Request) error { return errBoom },
			wantStatus: http.StatusInternalServerError,
			wantBody:   `{"message":"Internal Server Error"}` + "\n",
		},
		{
			name: "response written",
			handler: func(w http.ResponseWriter, _ *http.Request) error {
				return helper.WriteJSON(w, http.StatusCreated, map[string]int{"a": 1})
			},
			wantStatus: http.StatusCreated,
			wantBody:   `{"a":1}` + "\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			helper.Handle(tt.handler, false).ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/",
				nil))
			assert.Equal(t, tt.wantStatus, rec.Code)
			assert.Equal(t, tt.wantBody, rec.Body.String())
		})
	}
}

func TestBindBody(t *testing.T) {
	t.Parallel()

	type payload struct {
		A int `json:"a"`
	}

	tests := []struct {
		name        string
		contentType string
		body        string
		wantMessage string
		want        payload
		wantCode    int
	}{
		{name: "json", contentType: "application/json", body: `{"a":2}`, want: payload{A: 2}},
		{
			name:        "json with charset",
			contentType: "application/json; charset=utf-8",
			body:        `{"a":3}`,
			want:        payload{A: 3},
		},
		{name: "empty body is not bound", contentType: "text/plain", body: ""},
		{name: "other content type", contentType: "text/plain", body: `{"a":2}`,
			wantCode: http.StatusUnsupportedMediaType, wantMessage: "Unsupported Media Type"},
		{name: "invalid json", contentType: "application/json", body: `{`,
			wantCode: http.StatusBadRequest, wantMessage: "Bad Request"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", tt.contentType)

			var got payload

			err := helper.BindBody(req, &got)
			if tt.wantCode == 0 {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)

				return
			}

			var he *helper.HTTPError
			require.ErrorAs(t, err, &he)
			assert.Equal(t, tt.wantCode, he.Code)
			assert.Equal(t, tt.wantMessage, he.Message)

			// the same document, read before
			var raw payload

			rawErr := helper.BindRawBody(tt.contentType, []byte(tt.body), &raw)

			var rawHE *helper.HTTPError
			require.ErrorAs(t, rawErr, &rawHE)
			assert.Equal(t, tt.wantCode, rawHE.Code)
		})
	}
}

package helper_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/helper"
	"github.com/blackbirdworks/semaphore/internal/protocol"
)

const textPlain = "text/plain"

type bindTarget struct {
	Name string `json:"name"`
}

// failingReader fails every read with err.
type failingReader struct{ err error }

func (f failingReader) Read([]byte) (int, error) { return 0, f.err }

func TestBindBody_Outcomes(t *testing.T) {
	t.Parallel()

	errStatus := helper.NewHTTPError(http.StatusRequestEntityTooLarge, "too large")

	tests := []struct {
		body            func() *http.Request
		wantTarget      bindTarget
		name            string
		wantErrContains string
		wantStatus      int
	}{
		{
			name: "empty body leaves the target untouched",
			body: func() *http.Request {
				return httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", http.NoBody)
			},
		},
		{
			name: "json body is decoded",
			body: func() *http.Request {
				return newBindRequest(t, strings.NewReader(`{"name":"flag"}`), protocol.MIMEApplicationJSON, 15)
			},
			wantTarget: bindTarget{Name: "flag"},
		},
		{
			name: "json with parameters is decoded",
			body: func() *http.Request {
				return newBindRequest(t, strings.NewReader(`{"name":"flag"}`), "application/json; charset=utf-8", 15)
			},
			wantTarget: bindTarget{Name: "flag"},
		},
		{
			name: "another content type is a 415",
			body: func() *http.Request {
				return newBindRequest(t, strings.NewReader(`{"name":"flag"}`), textPlain, 15)
			},
			wantStatus:      http.StatusUnsupportedMediaType,
			wantErrContains: "Unsupported Media Type",
		},
		{
			name: "invalid json is a 400",
			body: func() *http.Request {
				return newBindRequest(t, strings.NewReader(`{"name":`), protocol.MIMEApplicationJSON, 8)
			},
			wantStatus:      http.StatusBadRequest,
			wantErrContains: "unexpected end of JSON input",
		},
		{
			name: "a body that can not be read is a 400",
			body: func() *http.Request {
				return newBindRequest(t, failingReader{err: errBoom}, protocol.MIMEApplicationJSON, 10)
			},
			wantStatus:      http.StatusBadRequest,
			wantErrContains: "boom",
		},
		{
			name: "a read error that has a status keeps it",
			body: func() *http.Request {
				return newBindRequest(t, failingReader{err: errStatus}, protocol.MIMEApplicationJSON, 10)
			},
			wantStatus:      http.StatusRequestEntityTooLarge,
			wantErrContains: "too large",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got bindTarget

			err := helper.BindBody(tt.body(), &got)
			assert.Equal(t, tt.wantTarget, got)

			if tt.wantStatus == 0 {
				require.NoError(t, err)

				return
			}

			require.Error(t, err)

			code, _ := helper.ErrorResponse(err, false)
			assert.Equal(t, tt.wantStatus, code)
			assert.ErrorContains(t, err, tt.wantErrContains)
		})
	}
}

func newBindRequest(t *testing.T, body io.Reader, contentType string, length int64,
) *http.Request {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", http.NoBody)
	req.Body = readCloser{body}
	req.ContentLength = length
	req.Header.Set(protocol.HeaderContentType, contentType)

	return req
}

type readCloser struct {
	io.Reader
}

func (readCloser) Close() error { return nil }

func TestBindRawBody(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		contentType string
		raw         string
		wantTarget  bindTarget
		wantStatus  int
	}{
		{name: "empty body", contentType: textPlain},
		{name: "json", contentType: protocol.MIMEApplicationJSON, raw: `{"name":"x"}`,
			wantTarget: bindTarget{Name: "x"}},
		{name: "not json", contentType: textPlain, raw: `{"name":"x"}`,
			wantStatus: http.StatusUnsupportedMediaType},
		{name: "invalid json", contentType: protocol.MIMEApplicationJSON, raw: `{`,
			wantStatus: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got bindTarget

			err := helper.BindRawBody(tt.contentType, []byte(tt.raw), &got)
			assert.Equal(t, tt.wantTarget, got)

			if tt.wantStatus == 0 {
				require.NoError(t, err)

				return
			}

			code, _ := helper.ErrorResponse(err, false)
			assert.Equal(t, tt.wantStatus, code)
		})
	}
}

package semaphore_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	controller "github.com/blackbirdworks/semaphore/cmd/relayproxy/handler/semaphore"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/helper"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/testdata/mock"
	"github.com/blackbirdworks/semaphore/internal/protocol"
)

func TestRequest_Resolve(t *testing.T) {
	t.Parallel()

	tests := []struct {
		headers         map[string]string
		name            string
		wantAPIKey      string
		wantContentType string
	}{
		{name: "no header"},
		{
			name: "X-API-Key and content type",
			headers: map[string]string{
				helper.XAPIKeyHeader: "key", protocol.HeaderContentType: protocol.MIMEApplicationJSON,
			},
			wantAPIKey:      "key",
			wantContentType: protocol.MIMEApplicationJSON,
		},
		{
			name:       "bearer token",
			headers:    map[string]string{"Authorization": "Bearer token-key"},
			wantAPIKey: "token-key",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", http.NoBody)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}

			var got controller.Request

			errs := got.Resolve(humatest.NewContext(&huma.Operation{}, req, httptest.NewRecorder()))

			assert.Empty(t, errs)
			assert.Equal(t, tt.wantAPIKey, got.APIKey())
			assert.Equal(t, tt.wantContentType, got.ContentType())
		})
	}
}

func TestBodyOpts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		readTimeout time.Duration
		want        time.Duration
	}{
		{name: "the read timeout of the server bounds the read", readTimeout: 30 * time.Second, want: 30 * time.Second},
		{name: "a zero read timeout means no bound", readTimeout: 0, want: -1},
		{name: "a negative read timeout means no bound", readTimeout: -time.Second, want: -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			operation := &huma.Operation{}
			controller.BodyOpts(operation, tt.readTimeout)

			assert.Equal(t, tt.want, operation.BodyReadTimeout)
			assert.Equal(t, int64(-1), operation.MaxBodyBytes)
			assert.True(t, operation.SkipValidateBody)
			assert.True(t, operation.SkipValidateParams)
		})
	}
}

func TestAllFlags_Handler_InvalidRequests(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		body        string
		contentType string
		wantMessage string
		wantStatus  int
	}{
		{
			name:        "the removed user field is refused",
			body:        `{"user":{"key":"u"}}`,
			contentType: protocol.MIMEApplicationJSON,
			wantStatus:  http.StatusBadRequest,
			wantMessage: "the 'user' field is no longer supported",
		},
		{
			name:        "a body without evaluationContext is refused",
			body:        `{}`,
			contentType: protocol.MIMEApplicationJSON,
			wantStatus:  http.StatusBadRequest,
			wantMessage: "impossible to find evaluationContext in request",
		},
		{
			name:        "a body that is not JSON is refused",
			body:        `evaluationContext`,
			contentType: "text/plain",
			wantStatus:  http.StatusUnsupportedMediaType,
			wantMessage: "Unsupported Media Type",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctrl := controller.NewAllFlags(&mock.Resolver{}, &metric.Metrics{})
			req := httptest.NewRequestWithContext(
				t.Context(), http.MethodPost, "/v1/allflags", strings.NewReader(tt.body))
			req.Header.Set(protocol.HeaderContentType, tt.contentType)

			err := ctrl.Handler(httptest.NewRecorder(), req)

			require.Error(t, err)

			code, body := helper.ErrorResponse(err, false)
			assert.Equal(t, tt.wantStatus, code)
			assert.Contains(t, err.Error(), tt.wantMessage)
			assert.NotEmpty(t, body.Message)
		})
	}
}

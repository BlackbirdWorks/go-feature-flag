package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api/middleware"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/testdata/mock"
)

// namedManager is a flag set manager that routes the keys of names to a flag set name.
type namedManager struct {
	mock.MockFlagsetManager

	names map[string]string
}

func (m *namedManager) FlagSetName(apiKey string) (string, error) {
	if name, ok := m.names[apiKey]; ok {
		return name, nil
	}

	return "", errNotFound
}

func TestStreamAuthorizer(t *testing.T) {
	t.Parallel()

	authenticated := func() mock.Keys { return mock.Keys{Evaluation: []string{"valid-api-key"}} }

	tests := []struct {
		manager     service.FlagSetResolver
		conf        mock.Keys
		name        string
		query       string
		wantBody    string
		wantStatus  int
		exposeError bool
	}{
		{name: "valid apiKey", conf: authenticated(), query: "?apiKey=valid-api-key", wantStatus: http.StatusOK},
		{
			name: "invalid apiKey", conf: authenticated(), query: "?apiKey=nope", wantStatus: http.StatusUnauthorized,
			wantBody: messageUnauthorized + "\n",
		},
		{
			name: "invalid apiKey, debug", conf: authenticated(), query: "?apiKey=nope", exposeError: true,
			wantStatus: http.StatusUnauthorized, wantBody: `{"message":"Unauthorized","error":"Unauthorized"}` + "\n",
		},
		{name: "authentication disabled", conf: mock.Keys{}, wantStatus: http.StatusOK},
		{
			name: "flag set key", conf: authenticated(), query: "?apiKey=k",
			manager: &namedManager{names: map[string]string{"k": "t"}}, wantStatus: http.StatusOK,
		},
		{
			name: "unknown flag set key", conf: authenticated(), query: "?apiKey=k",
			manager: &namedManager{}, wantStatus: http.StatusUnauthorized,
			wantBody: messageUnauthorized + "\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := middleware.StreamAuthorizer(service.NewAPIKeysFrom(tt.conf), tt.manager, tt.exposeError)(newOK())
			rec := serve(h, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/stream"+tt.query, nil))
			assert.Equal(t, tt.wantStatus, rec.Code)

			if tt.wantBody != "" {
				assert.Equal(t, tt.wantBody, rec.Body.String())
			}
		})
	}
}

// TestStreamAuthorizerObservesReload checks that the authorizer follows the keys of the configuration when they are
// replaced after it was built: a rotation (reload) takes effect on the next request.
func TestStreamAuthorizerObservesReload(t *testing.T) {
	t.Parallel()

	conf := mock.NewLiveKeys(mock.Keys{Evaluation: []string{"old-key"}})

	h := middleware.StreamAuthorizer(service.NewAPIKeysFrom(conf), nil, false)(newOK())
	status := func(key string) int {
		return serve(h, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/stream?apiKey="+key, nil)).Code
	}

	assert.Equal(t, http.StatusOK, status("old-key"))
	assert.Equal(t, http.StatusUnauthorized, status("new-key"))

	conf.Replace(mock.Keys{Evaluation: []string{"new-key"}})

	assert.Equal(t, http.StatusUnauthorized, status("old-key"))
	assert.Equal(t, http.StatusOK, status("new-key"))

	// authentication switched off by the reload: every request is accepted.
	conf.Replace(mock.Keys{})

	assert.Equal(t, http.StatusOK, status("old-key"))
}

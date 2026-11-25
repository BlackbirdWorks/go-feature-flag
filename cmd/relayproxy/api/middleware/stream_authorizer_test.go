package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api/middleware"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
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

	authenticated := func() *config.Config {
		c := &config.Config{AuthorizedKeys: config.APIKeys{Evaluation: []string{"valid-api-key"}}}
		c.ForceReloadAPIKeys()

		return c
	}

	tests := []struct {
		manager     service.FlagsetManager
		conf        *config.Config
		name        string
		query       string
		wantBody    string
		wantStatus  int
		exposeError bool
	}{
		{name: "valid apiKey", conf: authenticated(), query: "?apiKey=valid-api-key", wantStatus: http.StatusOK},
		{name: "invalid apiKey", conf: authenticated(), query: "?apiKey=nope", wantStatus: http.StatusUnauthorized,
			wantBody: messageUnauthorized + "\n"},
		{name: "invalid apiKey, debug", conf: authenticated(), query: "?apiKey=nope", exposeError: true,
			wantStatus: http.StatusUnauthorized, wantBody: `{"message":"Unauthorized","error":"Unauthorized"}` + "\n"},
		{name: "authentication disabled", conf: &config.Config{}, wantStatus: http.StatusOK},
		{name: "flag set key", conf: authenticated(), query: "?apiKey=k",
			manager: &namedManager{names: map[string]string{"k": "t"}}, wantStatus: http.StatusOK},
		{name: "unknown flag set key", conf: authenticated(), query: "?apiKey=k",
			manager: &namedManager{}, wantStatus: http.StatusUnauthorized,
			wantBody: messageUnauthorized + "\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := middleware.StreamAuthorizer(tt.conf, tt.manager, tt.exposeError)(newOK())
			rec := serve(h, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/stream"+tt.query, nil))
			assert.Equal(t, tt.wantStatus, rec.Code)

			if tt.wantBody != "" {
				assert.Equal(t, tt.wantBody, rec.Body.String())
			}
		})
	}
}

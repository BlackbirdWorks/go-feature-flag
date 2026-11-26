package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/apiwire"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/helper"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/testdata/mock"
)

// TestServer_AuthenticationObservesKeyReload checks that the keys of the configuration that a reload replaces in
// place are the ones the authentication middleware of a running server uses: the server holds a copy of the
// settings, not of the keys.
func TestServer_AuthenticationObservesKeyReload(t *testing.T) {
	t.Parallel()

	const (
		userPath  = "/v1/allflags"
		adminPath = "/admin/v1/flags/validate"
	)

	conf := &config.Config{AuthorizedKeys: config.APIKeys{
		Evaluation: []string{"old-user"},
		Admin:      []string{"old-admin"},
	}}
	conf.ForceReloadAPIKeys()

	srv := apiwire.New(conf, &service.Services{
		FlagsetManager: &mock.MockFlagsetManager{IsDefaultFlagSeItem: true},
		Metrics:        &metric.Metrics{},
	}, newTestLogger(t).Slog)

	// status is the status of a request with the key, 401 is the refusal of the authentication.
	status := func(path, key string) int {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, http.NoBody)
		if key != "" {
			req.Header.Set(helper.XAPIKeyHeader, key)
		}

		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)

		return rec.Code
	}

	assert.Equal(t, http.StatusUnauthorized, status(userPath, ""), "authentication is on")
	assert.NotEqual(t, http.StatusUnauthorized, status(userPath, "old-user"))
	assert.Equal(t, http.StatusUnauthorized, status(userPath, "new-user"))
	assert.NotEqual(t, http.StatusUnauthorized, status(adminPath, "old-admin"))
	assert.Equal(t, http.StatusUnauthorized, status(adminPath, "new-admin"))

	// the reload replaces the keys of the configuration in place.
	conf.ReplaceAPIKeys(config.APIKeys{Evaluation: []string{"new-user"}, Admin: []string{"new-admin"}})

	assert.Equal(t, http.StatusUnauthorized, status(userPath, "old-user"))
	assert.NotEqual(t, http.StatusUnauthorized, status(userPath, "new-user"))
	assert.Equal(t, http.StatusUnauthorized, status(adminPath, "old-admin"))
	assert.NotEqual(t, http.StatusUnauthorized, status(adminPath, "new-admin"))

	// a reload that leaves no evaluation key switches the authentication of the evaluation endpoints off.
	conf.ReplaceAPIKeys(config.APIKeys{Admin: []string{"new-admin"}})

	assert.NotEqual(t, http.StatusUnauthorized, status(userPath, ""))
	assert.Equal(t, http.StatusUnauthorized, status(adminPath, ""))
}

package api_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/apiwire"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/helper"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/cmdhelpers/retrieverconf"
	"github.com/blackbirdworks/semaphore/internal/protocol"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

// TestServer_AuthenticationObservesKeyReload checks that the keys of the configuration that a reload replaces in
// place are the ones the authentication middleware of a running server uses: the server holds a copy of the
// settings, not of the keys. The requests are well formed: an accepted key gets the success of the endpoint, a
// refused one the 401 of the authentication, so a request that fails for another reason can not pass for a success.
func TestServer_AuthenticationObservesKeyReload(t *testing.T) {
	t.Parallel()

	const (
		evaluatePath = "/ofrep/v1/evaluate/flags/test-flag"
		evaluateBody = `{"context":{"targetingKey":"user-1"}}`
		validatePath = "/admin/v1/flags/validate"
		oldUser      = "old-user"
		newUser      = "new-user"
		oldAdmin     = "old-admin"
		newAdmin     = "new-admin"
		validateBody = `{"my-flag":{"variations":{"a":true,"b":false},"defaultRule":{"variation":"a"}}}`
	)

	conf := &config.Config{
		AuthorizedKeys:  config.APIKeys{Evaluation: []string{oldUser}, Admin: []string{oldAdmin}},
		PollingInterval: 10000,
		FileFormat:      protocol.FormatYAML,
		Retrievers: &[]retrieverconf.RetrieverConf{
			{Kind: retrieverconf.FileRetriever, Path: fixtures.FlagConfigYAMLParentPath},
		},
	}
	conf.ForceReloadAPIKeys()

	log := newTestLogger(t)

	manager, err := service.NewFlagsetManager(conf, log.Slog, nil, nil, nil)
	require.NoError(t, err)
	t.Cleanup(manager.Close)

	srv := apiwire.New(conf, &service.Services{FlagsetManager: manager, Metrics: &metric.Metrics{}}, log.Slog)

	// status is the status of the request that the endpoint of the path gets with the key.
	status := func(path, key string) int {
		body := evaluateBody
		if path == validatePath {
			body = validateBody
		}

		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, strings.NewReader(body))
		req.Header.Set(protocol.HeaderContentType, protocol.MIMEApplicationJSON)

		if key != "" {
			req.Header.Set(helper.XAPIKeyHeader, key)
		}

		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)

		return rec.Code
	}

	type request struct {
		name       string
		path       string
		key        string
		wantStatus int
	}

	check := func(t *testing.T, phase string, requests []request) {
		t.Helper()

		for _, r := range requests {
			assert.Equal(t, r.wantStatus, status(r.path, r.key), "%s: %s", phase, r.name)
		}
	}

	check(t, "before the reload", []request{
		{"no evaluation key", evaluatePath, "", http.StatusUnauthorized},
		{"evaluation key", evaluatePath, oldUser, http.StatusOK},
		{"unknown evaluation key", evaluatePath, newUser, http.StatusUnauthorized},
		{"admin key", validatePath, oldAdmin, http.StatusOK},
		{"unknown admin key", validatePath, newAdmin, http.StatusUnauthorized},
	})

	// the reload replaces the keys of the configuration in place.
	conf.ReplaceAPIKeys(config.APIKeys{Evaluation: []string{newUser}, Admin: []string{newAdmin}})

	check(t, "after the rotation", []request{
		{"old evaluation key", evaluatePath, oldUser, http.StatusUnauthorized},
		{"new evaluation key", evaluatePath, newUser, http.StatusOK},
		{"old admin key", validatePath, oldAdmin, http.StatusUnauthorized},
		{"new admin key", validatePath, newAdmin, http.StatusOK},
	})

	// a reload that leaves no evaluation key switches the authentication of the evaluation endpoints off, the admin
	// endpoints always need their key.
	conf.ReplaceAPIKeys(config.APIKeys{Admin: []string{newAdmin}})

	check(t, "after the evaluation keys were removed", []request{
		{"no key on an evaluation endpoint", evaluatePath, "", http.StatusOK},
		{"no key on an admin endpoint", validatePath, "", http.StatusUnauthorized},
		{"admin key", validatePath, newAdmin, http.StatusOK},
	})
}

package api_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service/stream"
	"github.com/blackbirdworks/semaphore/cmdhelpers/retrieverconf"
	"github.com/blackbirdworks/semaphore/testutils"
)

// TestFlagsetMode_EvaluationKeysOutsideFlagsets checks that, in flag set mode, a key that is not
// linked to a flag set is rejected by the authentication of every evaluation endpoint, instead of
// being accepted and failing later on the flag set lookup.
func TestFlagsetMode_EvaluationKeysOutsideFlagsets(t *testing.T) {
	t.Parallel()

	listener, port := testutils.ListenLocal(t)
	proxyConf := &config.Config{
		FlagSets: []config.FlagSet{{
			Name:      "tenant",
			APIKeys:   []string{"flagset-key"},
			Retriever: &retrieverconf.RetrieverConf{Kind: "file", Path: "../../../testdata/flag-config.yaml"},
		}},
		AuthorizedKeys: config.APIKeys{
			Evaluation: []string{"authorized-evaluation-key"},
			Admin:      []string{"admin-key"},
		},
		Server: config.Server{Mode: config.ServerModeHTTP, Port: port},
	}
	proxyConf.ForceReloadAPIKeys()

	log := newTestLogger(t)

	wsService := stream.NewWebsocketService()
	t.Cleanup(wsService.Close)

	sseService := stream.NewSSEService()
	t.Cleanup(sseService.Close)
	flagsetManager, err := service.NewFlagsetManager(proxyConf, log.Slog, nil, sseService, wsService)
	require.NoError(t, err)
	t.Cleanup(flagsetManager.Close)

	srv := newTestServer(t, proxyConf, service.Services{
		MonitoringService: service.NewMonitoring(flagsetManager),
		WebsocketService:  wsService,
		SSEService:        sseService,
		FlagsetManager:    flagsetManager,
		Metrics:           metric.Metrics{},
	}, log.Slog, api.WithListener(listener))
	go srv.StartWithContext(context.Background())

	t.Cleanup(func() { srv.Stop(context.Background()) })

	baseURL := fmt.Sprintf("http://localhost:%d", port)
	waitForServer(t, baseURL)

	const evalBody = `{"context":{"targetingKey":"user-1"}}`

	endpoints := []struct {
		name   string
		method string
		path   string
		body   string
		// keyInQuery is true for the streaming endpoints, which read the key from the apiKey query param.
		keyInQuery bool
	}{
		{name: "ofrep single", method: http.MethodPost, path: "/ofrep/v1/evaluate/flags/test-flag", body: evalBody},
		{name: "ofrep bulk", method: http.MethodPost, path: "/ofrep/v1/evaluate/flags", body: evalBody},
		{name: "eval", method: http.MethodPost, path: "/v1/feature/test-flag/eval",
			body: `{"evaluationContext":{"key":"user-1"},"defaultValue":false}`},
		{name: "allflags", method: http.MethodPost, path: "/v1/allflags",
			body: `{"evaluationContext":{"key":"user-1"}}`},
		{name: "flag configuration", method: http.MethodPost, path: "/v1/flag/configuration", body: `{}`},
		{name: "flag change", method: http.MethodGet, path: "/v1/flag/change"},
		{name: "data collector", method: http.MethodPost, path: "/v1/data/collector", body: `{"events":[]}`},
		{name: "manifest", method: http.MethodGet, path: "/openfeature/v0/manifest"},
		{name: "websocket", method: http.MethodGet, path: "/stream/v1/ws/flag/change", keyInQuery: true},
		{name: "sse", method: http.MethodGet, path: "/stream/v1/sse/flag/change", keyInQuery: true},
	}
	keys := []struct {
		name string
		key  string
		want int
		// skipStreams is true when the key is accepted: a stream would then stay open.
		skipStreams bool
	}{
		{name: "flag set", key: "flagset-key", want: http.StatusOK, skipStreams: true},
		// An admin key authenticates but it is not linked to a flag set: documented 400.
		{name: "admin", key: "admin-key", want: http.StatusBadRequest},
		{name: "authorizedKeys.evaluation", key: "authorized-evaluation-key", want: http.StatusUnauthorized},
		{name: "unknown", key: "unknown-key", want: http.StatusUnauthorized},
	}

	for _, endpoint := range endpoints {
		for _, keyCase := range keys {
			t.Run(endpoint.name+" with "+keyCase.name+" key", func(t *testing.T) {
				t.Parallel()

				if endpoint.keyInQuery && keyCase.skipStreams {
					t.Skip("a stream stays open when the key is accepted")
				}

				target := baseURL + endpoint.path
				if endpoint.keyInQuery {
					target += "?apiKey=" + url.QueryEscape(keyCase.key)
				}

				req, requestWithContextErr := http.NewRequestWithContext(
					t.Context(), endpoint.method, target, strings.NewReader(endpoint.body))
				require.NoError(t, requestWithContextErr)
				req.Header.Set("Content-Type", "application/json")

				if !endpoint.keyInQuery {
					req.Header.Set("X-Api-Key", keyCase.key)
				}

				resp, requestWithContextErr := http.DefaultClient.Do(req)
				require.NoError(t, requestWithContextErr)

				defer func() { _ = resp.Body.Close() }()

				body, requestWithContextErr := io.ReadAll(resp.Body)
				require.NoError(t, requestWithContextErr)

				assert.Equal(t, keyCase.want, resp.StatusCode, string(body))
			})
		}
	}
}

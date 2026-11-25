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
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/helper"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service/stream"
	"github.com/blackbirdworks/semaphore/cmdhelpers/retrieverconf"
	"github.com/blackbirdworks/semaphore/internal/protocol"
	"github.com/blackbirdworks/semaphore/testhelpers"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

// TestFlagsetMode_EvaluationKeysOutsideFlagsets checks that, in flag set mode, a key that is not
// linked to a flag set is rejected by the authentication of every evaluation endpoint, instead of
// being accepted and failing later on the flag set lookup.
func TestFlagsetMode_EvaluationKeysOutsideFlagsets(t *testing.T) {
	t.Parallel()

	listener, port := testhelpers.ListenLocal(t)
	proxyConf := &config.Config{
		FlagSets: []config.FlagSet{{
			Name:      "tenant",
			APIKeys:   []string{"flagset-key"},
			Retriever: &retrieverconf.RetrieverConf{Kind: fixtures.File, Path: fixtures.FlagConfigYAMLParentPath},
		}},
		AuthorizedKeys: config.APIKeys{
			Evaluation: []string{"authorized-evaluation-key"},
			Admin:      []string{adminKey},
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

	baseURL := fmt.Sprintf(httpLocalhostD, port)
	waitForServer(t, baseURL)

	const evalBody = contextTargetingKeyUserOne

	endpoints := []struct {
		name   string
		method string
		path   string
		body   string
		// keyInQuery is true for the streaming endpoints, which read the key from the apiKey query param.
		keyInQuery bool
	}{
		{name: "ofrep single", method: http.MethodPost, path: ofrepEvaluateTestFlag, body: evalBody},
		{name: "ofrep bulk", method: http.MethodPost, path: fixtures.OFREPEvaluateFlagsPath, body: evalBody},
		{name: "eval", method: http.MethodPost, path: v1FeatureTest,
			body: `{"evaluationContext":{"key":"user-1"},"defaultValue":false}`},
		{name: "allflags", method: http.MethodPost, path: v1Allflags,
			body: `{"evaluationContext":{"key":"user-1"}}`},
		{name: "flag configuration", method: http.MethodPost, path: v1FlagConfiguration, body: `{}`},
		{name: "flag change", method: http.MethodGet, path: v1FlagChange},
		{name: "data collector", method: http.MethodPost, path: v1DataCollector, body: emptyEventsBody},
		{name: "manifest", method: http.MethodGet, path: openfeatureV0Manifest},
		{name: "websocket", method: http.MethodGet, path: streamV1Ws, keyInQuery: true},
		{name: "sse", method: http.MethodGet, path: streamV1Sse, keyInQuery: true},
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
		{name: fixtures.UserAdmin, key: adminKey, want: http.StatusBadRequest},
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
					target += apiKeyQuery + url.QueryEscape(keyCase.key)
				}

				req, requestWithContextErr := http.NewRequestWithContext(
					t.Context(), endpoint.method, target, strings.NewReader(endpoint.body))
				require.NoError(t, requestWithContextErr)
				req.Header.Set(protocol.HeaderContentType, protocol.MIMEApplicationJSON)

				if !endpoint.keyInQuery {
					req.Header.Set(helper.XAPIKeyHeader, keyCase.key)
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

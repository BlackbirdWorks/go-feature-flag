package api_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ffclient "github.com/blackbirdworks/semaphore"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service/stream"
	"github.com/blackbirdworks/semaphore/cmdhelpers/retrieverconf"
	"github.com/blackbirdworks/semaphore/testutils"
)

// routingLostManager is a flag set manager whose routing table does not know the keys of lostKeys anymore,
// as right after a reload removed them, while everything else is delegated to the real manager.
type routingLostManager struct {
	service.FlagsetManager
	lostKeys map[string]bool
}

func (m routingLostManager) FlagSet(apiKey string) (*ffclient.GoFeatureFlag, error) {
	if m.lostKeys[apiKey] {
		return nil, errors.New("flagset not found for API key")
	}

	return m.FlagsetManager.FlagSet(apiKey)
}

func (m routingLostManager) FlagSetName(apiKey string) (string, error) {
	if m.lostKeys[apiKey] {
		return "", errors.New("no flag set associated to the API key")
	}

	return m.FlagsetManager.FlagSetName(apiKey)
}

// TestFlagsetMode_KeyWithoutRoutingIsRejectedAtAuthentication checks that authentication and flag set routing
// use the same table: a key that does not route to a flag set (anymore) is rejected with 401 by every
// endpoint, it is never authenticated and then failed with a 400 by the handler. The routing is swapped
// in one step by a reload, the accepted key set must not be a second table that can disagree with it.
func TestFlagsetMode_KeyWithoutRoutingIsRejectedAtAuthentication(t *testing.T) {
	t.Parallel()

	ln, port := testutils.ListenLocal(t)
	proxyConf := &config.Config{
		FlagSets: []config.FlagSet{{
			Name:      "tenant",
			APIKeys:   []string{"routed-key", "moved-key"},
			Retriever: &retrieverconf.RetrieverConf{Kind: "file", Path: "../../../testdata/flag-config.yaml"},
		}},
		Server: config.Server{Mode: config.ServerModeHTTP, Port: port},
	}
	proxyConf.ForceReloadAPIKeys()
	log := newTestLogger(t)

	wsService := stream.NewWebsocketService()
	t.Cleanup(wsService.Close)
	sseService := stream.NewSSEService()
	t.Cleanup(sseService.Close)
	realManager, err := service.NewFlagsetManager(proxyConf, log.ZapLogger, nil, sseService, wsService)
	require.NoError(t, err)
	t.Cleanup(realManager.Close)

	s := api.New(proxyConf, service.Services{
		MonitoringService: service.NewMonitoring(realManager),
		WebsocketService:  wsService,
		SSEService:        sseService,
		FlagsetManager: routingLostManager{
			FlagsetManager: realManager, lostKeys: map[string]bool{"moved-key": true},
		},
		Metrics: metric.Metrics{},
	}, log.ZapLogger, api.WithListener(ln))
	go s.StartWithContext(context.Background())
	t.Cleanup(func() { s.Stop(context.Background()) })
	baseURL := fmt.Sprintf("http://localhost:%d", port)
	waitForServer(t, baseURL)

	endpoints := []struct {
		name       string
		method     string
		path       string
		body       string
		keyInQuery bool
	}{
		{name: "ofrep single", method: http.MethodPost, path: "/ofrep/v1/evaluate/flags/test-flag",
			body: `{"context":{"targetingKey":"user-1"}}`},
		{name: "ofrep bulk", method: http.MethodPost, path: "/ofrep/v1/evaluate/flags",
			body: `{"context":{"targetingKey":"user-1"}}`},
		{name: "allflags", method: http.MethodPost, path: "/v1/allflags",
			body: `{"evaluationContext":{"key":"user-1"}}`},
		{name: "websocket", method: http.MethodGet, path: "/stream/v1/ws/flag/change", keyInQuery: true},
		{name: "sse", method: http.MethodGet, path: "/stream/v1/sse/flag/change", keyInQuery: true},
	}
	keys := []struct {
		name string
		key  string
		want int
	}{
		{name: "routed key", key: "routed-key", want: http.StatusOK},
		{name: "key that lost its routing", key: "moved-key", want: http.StatusUnauthorized},
	}
	for _, ep := range endpoints {
		for _, k := range keys {
			t.Run(ep.name+" with "+k.name, func(t *testing.T) {
				t.Parallel()
				if ep.keyInQuery && k.want == http.StatusOK {
					t.Skip("a stream stays open when the key is accepted")
				}

				target := baseURL + ep.path
				if ep.keyInQuery {
					target += "?apiKey=" + url.QueryEscape(k.key)
				}
				req, requestWithContextErr := http.NewRequestWithContext(
					t.Context(),
					ep.method,
					target,
					strings.NewReader(ep.body),
				)
				require.NoError(t, requestWithContextErr)
				req.Header.Set("Content-Type", "application/json")
				if !ep.keyInQuery {
					req.Header.Set("X-Api-Key", k.key)
				}
				resp, requestWithContextErr := http.DefaultClient.Do(req)
				require.NoError(t, requestWithContextErr)
				defer func() { _ = resp.Body.Close() }()
				body, requestWithContextErr := io.ReadAll(resp.Body)
				require.NoError(t, requestWithContextErr)

				assert.Equal(t, k.want, resp.StatusCode, string(body))
			})
		}
	}
}

// TestFlagsetMode_KeyRotationDuringRequests runs requests while reloads rotate and move flag set keys, under
// the race detector. A key is always either routed to a flag set that holds it or rejected with 401: it is never
// authenticated and then failed with a 400 because the routing changed in between.
func TestFlagsetMode_KeyRotationDuringRequests(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	flagFile := func(name, flag string) string {
		path := filepath.Join(dir, name+".yaml")
		content := flag + ":\n  variations:\n    on: true\n    off: false\n  defaultRule:\n    variation: on\n"
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

		return path
	}
	fileA, fileB := flagFile("a", "flag-a"), flagFile("b", "flag-b")
	flagsets := func(keysA, keysB []string) []config.FlagSet {
		return []config.FlagSet{
			{Name: "a", APIKeys: keysA, Retriever: &retrieverconf.RetrieverConf{Kind: "file", Path: fileA}},
			{Name: "b", APIKeys: keysB, Retriever: &retrieverconf.RetrieverConf{Kind: "file", Path: fileB}},
		}
	}
	// rotating-key moves from a to b, old-key is replaced by new-key.
	// The states are built for each use: a reload stores the keys of the configuration it applies.
	stateOne := func() []config.FlagSet {
		return flagsets([]string{"a-key", "rotating-key", "old-key"}, []string{"b-key"})
	}
	stateTwo := func() []config.FlagSet {
		return flagsets([]string{"a-key", "new-key"}, []string{"b-key", "rotating-key"})
	}

	ln, port := testutils.ListenLocal(t)
	proxyConf := &config.Config{FlagSets: stateOne(), Server: config.Server{Mode: config.ServerModeHTTP, Port: port}}
	proxyConf.ForceReloadAPIKeys()
	log := newTestLogger(t)
	manager, err := service.NewFlagsetManager(proxyConf, log.ZapLogger, nil, nil, nil)
	require.NoError(t, err)
	t.Cleanup(manager.Close)

	s := api.New(proxyConf, service.Services{
		MonitoringService: service.NewMonitoring(manager),
		FlagsetManager:    manager,
		Metrics:           metric.Metrics{},
	}, log.ZapLogger, api.WithListener(ln))
	go s.StartWithContext(context.Background())
	t.Cleanup(func() { s.Stop(context.Background()) })
	baseURL := fmt.Sprintf("http://localhost:%d", port)
	waitForServer(t, baseURL)

	// The statuses a key can get: routed to a flag set holding it (200, or 404 when the flag lives in the
	// other flag set), or rejected (401). Anything else, and 400 in particular, is an inconsistent state.
	wantStatuses := map[string][]int{
		"rotating-key": {http.StatusOK, http.StatusNotFound},
		"old-key":      {http.StatusOK, http.StatusUnauthorized},
		"new-key":      {http.StatusOK, http.StatusUnauthorized},
	}
	done := make(chan struct{})
	var unexpected, requests atomic.Int64
	var wg, firstRequests sync.WaitGroup
	firstRequests.Add(len(wantStatuses))
	for key, want := range wantStatuses {
		wg.Go(func() {
			client := &http.Client{}
			var once sync.Once
			// the reloads start once every worker is sending requests (or gave up)
			signalFirst := func() { once.Do(firstRequests.Done) }
			defer signalFirst()
			for {
				select {
				case <-done:
					return
				default:
				}
				req, requestErr := http.NewRequest(http.MethodPost, baseURL+"/ofrep/v1/evaluate/flags/flag-a",
					strings.NewReader(`{"context":{"targetingKey":"user-1"}}`))
				if requestErr != nil {
					unexpected.Add(1)

					return
				}
				req.Header.Set("X-Api-Key", key)
				req.Header.Set("Content-Type", "application/json")
				resp, requestErr := client.Do(req)
				if requestErr != nil {
					unexpected.Add(1)

					return
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				requests.Add(1)
				signalFirst()
				if !slices.Contains(want, resp.StatusCode) {
					unexpected.Add(1)
				}
			}
		})
	}
	firstRequests.Wait()
	for i := range 301 {
		// Every reload overlaps requests: wait for one that completes after it started.
		completedBefore := requests.Load()
		if i%2 == 0 {
			manager.OnConfigChange(&config.Config{FlagSets: stateTwo()})
		} else {
			manager.OnConfigChange(&config.Config{FlagSets: stateOne()})
		}
		for requests.Load() == completedBefore && unexpected.Load() == 0 {
			runtime.Gosched()
		}
	}
	close(done)
	wg.Wait()

	assert.Equal(t, int64(0), unexpected.Load(), "a key was authenticated and then failed, or got a wrong flag set")
	assert.Positive(t, requests.Load())

	// The last reload (an even iteration) applied the second state, which differs from the startup state: the
	// keys really rotated and moved.
	_, errOld := manager.FlagSet("old-key")
	_, errNew := manager.FlagSet("new-key")
	nameRotating, errRotating := manager.FlagSetName("rotating-key")
	assert.Error(t, errOld)        //nolint:testifylint
	assert.NoError(t, errNew)      //nolint:testifylint
	assert.NoError(t, errRotating) //nolint:testifylint
	assert.Equal(t, "b", nameRotating)
}

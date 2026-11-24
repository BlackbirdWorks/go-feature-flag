//go:build docker

package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"text/template"
	"time"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service/stream"
	"github.com/blackbirdworks/semaphore/notifier"
	"github.com/blackbirdworks/semaphore/testutils"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

// startRelayProxyFromYAML starts a relay proxy in the process from a YAML configuration, wired like
// cmd/relayproxy/main.go does (config file -> flagset manager -> API server). The template is
// executed with data, and the port of the server is available as {{ .Port }}. It returns the base URL.
func startRelayProxyFromYAML(t *testing.T, yamlTemplate string, data map[string]any) string {
	t.Helper()
	port := testutils.GetFreePort(t)
	if data == nil {
		data = map[string]any{}
	}
	data["Port"] = port
	tpl, err := template.New("conf").Parse(yamlTemplate)
	require.NoError(t, err)
	var buf bytes.Buffer
	require.NoError(t, tpl.Execute(&buf, data))
	confPath := filepath.Join(t.TempDir(), "goff-proxy.yaml")
	require.NoError(t, os.WriteFile(confPath, buf.Bytes(), 0o600))

	log := newTestLogger(t)
	f := pflag.NewFlagSet("config", pflag.ContinueOnError)
	f.String("config", "", "")
	require.NoError(t, f.Parse([]string{"--config", confPath}))
	proxyConf, err := config.New(f, log.ZapLogger, "e2e")
	require.NoError(t, err)
	t.Cleanup(func() { _ = proxyConf.StopConfigChangeWatcher() })
	require.NoError(t, proxyConf.IsValid(), "config:\n%s", buf.String())

	metrics, err := metric.NewMetrics()
	require.NoError(t, err)
	ws := stream.NewWebsocketService()
	sse := stream.NewSSEService()
	manager, err := service.NewFlagsetManager(proxyConf, log.ZapLogger,
		[]notifier.Notifier{metric.NewPrometheusNotifier(metrics)}, sse, ws)
	require.NoError(t, err)
	services := service.Services{
		MonitoringService: service.NewMonitoring(manager),
		WebsocketService:  ws,
		SSEService:        sse,
		FlagsetManager:    manager,
		Metrics:           metrics,
	}
	s := api.New(proxyConf, services, log.ZapLogger)
	go func() { s.StartWithContext(context.Background()) }()
	// same order as main.shutdown: the exporters are flushed when the flagsets are closed.
	t.Cleanup(func() {
		ws.Close()
		s.Stop(context.Background())
		manager.Close()
	})
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	waitForServer(t, baseURL)
	return baseURL
}

// apiResponse is a decoded HTTP response.
type apiResponse struct {
	Status int
	Body   map[string]any
	Raw    string
}

// postJSON sends a JSON body and decodes the JSON object of the response.
func postJSON(t testing.TB, url, apiKey string, body any) apiResponse {
	t.Helper()
	b, err := json.Marshal(body)
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, bytes.NewReader(b))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	res := apiResponse{Status: resp.StatusCode, Raw: string(raw)}
	_ = json.Unmarshal(raw, &res.Body) // not JSON for some errors, Body stays nil
	return res
}

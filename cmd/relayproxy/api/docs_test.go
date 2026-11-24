package api_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
)

// newDocsServer builds a server with the Swagger UI enabled and returns its API handler.
func newDocsServer(t *testing.T, monitoringPort int) http.Handler {
	t.Helper()
	conf := testConfig(t)
	conf.Version = "v9.9.9"
	conf.Swagger.Enabled = true
	conf.Server.MonitoringPort = monitoringPort
	flagsetManager, err := service.NewFlagsetManager(conf, newTestLogger(t).Slog, nil, nil, nil)
	require.NoError(t, err)
	t.Cleanup(flagsetManager.Close)
	srv := api.New(conf, service.Services{
		MonitoringService: service.NewMonitoring(flagsetManager),
		FlagsetManager:    flagsetManager,
	}, newTestLogger(t).Slog)

	return srv.Handler()
}

// Test_Swagger_Document checks the document that the Swagger UI displays: it describes the whole API (the
// monitoring endpoints included, whether they are served by the same listener or by a dedicated one), carries the
// version of the relay proxy and advertises no server, so that "Try it out" calls the origin of the page.
func Test_Swagger_Document(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		monitoringPort int
	}{
		{name: "one listener"},
		{name: "dedicated monitoring listener", monitoringPort: 18080},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := newDocsServer(t, tt.monitoringPort)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/swagger/openapi.json", nil))
			require.Equal(t, http.StatusOK, rec.Code)

			var doc struct {
				Paths      map[string]map[string]any `json:"paths"`
				Components struct {
					Schemas map[string]any `json:"schemas"`
				} `json:"components"`
				Info struct {
					Version string `json:"version"`
				} `json:"info"`
				Servers []any `json:"servers"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &doc))
			assert.Equal(t, "v9.9.9", doc.Info.Version)
			assert.Empty(t, doc.Servers)
			for _, path := range []string{"/health", "/info", "/metrics", "/v1/allflags", "/admin/v1/flags/validate"} {
				assert.Contains(t, doc.Paths, path)
			}
			assert.Contains(t, doc.Components.Schemas, "HealthResponse")
			assert.Contains(t, doc.Components.Schemas, "AllFlagRequest")
		})
	}
}

// Test_Swagger_ConcurrentFirstRequests fires the first requests of every document at once: huma renders a document
// on its first request into a variable it shares, the server renders them before it serves (run with -race).
func Test_Swagger_ConcurrentFirstRequests(t *testing.T) {
	t.Parallel()

	h := newDocsServer(t, 0)
	var wg sync.WaitGroup
	for _, path := range []string{
		"/swagger/openapi.json", "/swagger/openapi-3.0.json", "/swagger/openapi.yaml", "/swagger/openapi-3.0.yaml",
	} {
		for range 8 {
			wg.Go(func() {
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
				assert.Equal(t, http.StatusOK, rec.Code)
				assert.NotEmpty(t, rec.Body.Bytes())
			})
		}
	}
	wg.Wait()
}

// Test_Upload_ReadIsBound checks that an upload that stalls is ended by the read timeout, for the two endpoints that
// read their own body: the read of a body is never unbounded.
func Test_Upload_ReadIsBound(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
	}{
		{name: "data collector", path: "/v1/data/collector"},
		{name: "flag configuration", path: "/v1/flag/configuration"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			conf := testConfig(t)
			baseURL := startGoldenServer(t, conf, api.WithReadTimeout(300*time.Millisecond))
			addr := baseURL[len("http://"):]
			conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(t.Context(), "tcp", addr)
			require.NoError(t, err)
			t.Cleanup(func() { _ = conn.Close() })

			// the headers announce a body that never arrives in full
			_, err = fmt.Fprintf(conn, "POST %s HTTP/1.1\r\nHost: x\r\nContent-Type: application/json\r\n"+
				"Content-Length: 100000\r\n\r\n{\"events\":[", tt.path)
			require.NoError(t, err)

			start := time.Now()
			require.NoError(t, conn.SetReadDeadline(start.Add(10*time.Second)))
			// the server ends the exchange: it answers, or closes the connection, but it does not wait for ever.
			_, _ = bufio.NewReader(conn).ReadString('\n')
			assert.Less(t, time.Since(start), 5*time.Second, "a stalled upload is not ended")
		})
	}
}

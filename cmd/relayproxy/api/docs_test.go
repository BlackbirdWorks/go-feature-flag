package api_test

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api"
	controller "github.com/blackbirdworks/semaphore/cmd/relayproxy/handler/semaphore"
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
			// the schemas of the error model of huma are not used by any operation
			assert.NotContains(t, doc.Components.Schemas, "ErrorModel")
			assert.NotContains(t, doc.Components.Schemas, "ErrorDetail")
		})
	}
}

// Test_Swagger_ConcurrentFirstRequests fires the first requests of every document at once: huma renders a document
// on its first request into a variable it shares, the server renders them before it serves (run with -race).
func Test_Swagger_ConcurrentFirstRequests(t *testing.T) {
	t.Parallel()

	h := newDocsServer(t, 0)
	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		bodies = map[string][]string{}
	)
	paths := []string{
		"/swagger/openapi.json", "/swagger/openapi-3.0.json", "/swagger/openapi.yaml", "/swagger/openapi-3.0.yaml",
	}
	for _, path := range paths {
		for range 8 {
			wg.Go(func() {
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
				assert.Equal(t, http.StatusOK, rec.Code)
				mu.Lock()
				bodies[path] = append(bodies[path], rec.Body.String())
				mu.Unlock()
			})
		}
	}
	wg.Wait()

	// the race detector reports the unsynchronised cache; every request must also have got the same complete document.
	for _, path := range paths {
		require.Len(t, bodies[path], 8)
		assert.NotEmpty(t, bodies[path][0])
		for _, body := range bodies[path] {
			assert.Equal(t, bodies[path][0], body, path)
		}
	}
}

// Test_Upload_ReadIsBound checks end to end that the routes that read their own body end a stalled upload (the
// headers announce a body that never arrives in full) when the server has a read timeout: it answers 408 or closes
// the connection. The pass condition is the outcome: a client-side timeout fails the test, the generous client
// deadline only keeps a hang from blocking it. That the timeout is what is applied, and that it is absent when the
// option disables it, is checked at the mechanism by Test_Upload_ReadDeadline.
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
			baseURL := startGoldenServer(t, conf, api.WithReadTimeout(100*time.Millisecond))
			conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(
				t.Context(),
				"tcp",
				baseURL[len("http://"):],
			)
			require.NoError(t, err)
			t.Cleanup(func() { _ = conn.Close() })

			_, err = fmt.Fprintf(conn, "POST %s HTTP/1.1\r\nHost: x\r\nContent-Type: application/json\r\n"+
				"Content-Length: 100000\r\n\r\n{\"events\":[", tt.path)
			require.NoError(t, err, "the request was sent in full")

			require.NoError(t, conn.SetReadDeadline(time.Now().Add(30*time.Second)))
			line, err := bufio.NewReader(conn).ReadString('\n')
			var netErr net.Error
			require.False(t, errors.As(err, &netErr) && netErr.Timeout(), "the server never ended the stalled upload")
			if err == nil {
				assert.Contains(t, line, "408", "the answer to a stalled upload is a 408")

				return
			}
			// the server closed the connection (EOF, or a reset when it closes with data unread)
			assert.True(t, errors.Is(err, io.EOF) || errors.Is(err, syscall.ECONNRESET), "unexpected error: %v", err)
		})
	}
}

// deadlineRecorder is a response writer that records the read deadlines that are set on the connection (huma sets
// them through the writer, following Unwrap): it shows what the registered routes ask of the server.
type deadlineRecorder struct {
	*httptest.ResponseRecorder

	deadlines []time.Time
}

func (d *deadlineRecorder) SetReadDeadline(deadline time.Time) error {
	d.deadlines = append(d.deadlines, deadline)

	return nil
}

// Test_Upload_ReadDeadline checks the read deadline that the registered upload routes set for the body, through the
// whole chain of middleware: the read timeout of the server when it is on, no deadline (a zero time clears the
// deadline of the connection, so the read is not bound at all, and huma's default of 5 seconds is not applied) when
// the option disables it. It is the proof that the "off" case is really unbounded, without waiting for anything.
func Test_Upload_ReadDeadline(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		path    string
		timeout time.Duration
		wantOn  bool
	}{
		{name: "data collector, timeout on", path: "/v1/data/collector", timeout: 100 * time.Millisecond, wantOn: true},
		{
			name:    "flag configuration, timeout on",
			path:    "/v1/flag/configuration",
			timeout: 100 * time.Millisecond,
			wantOn:  true,
		},
		{name: "data collector, timeout off", path: "/v1/data/collector"},
		{name: "flag configuration, timeout off", path: "/v1/flag/configuration"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			conf := testConfig(t)
			flagsetManager, err := service.NewFlagsetManager(conf, newTestLogger(t).Slog, nil, nil, nil)
			require.NoError(t, err)
			t.Cleanup(flagsetManager.Close)
			srv := api.New(conf, service.Services{FlagsetManager: flagsetManager}, newTestLogger(t).Slog,
				api.WithReadTimeout(tt.timeout))

			req := httptest.NewRequestWithContext(
				t.Context(),
				http.MethodPost,
				tt.path,
				strings.NewReader(`{"events":[]}`),
			)
			req.Header.Set("Content-Type", "application/json")
			rec := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
			before := time.Now()
			srv.Handler().ServeHTTP(rec, req)
			after := time.Now()

			require.NotEmpty(
				t,
				rec.deadlines,
				"the route set no read deadline: the chain hides the connection from huma",
			)
			first := rec.deadlines[0]
			if !tt.wantOn {
				assert.True(t, first.IsZero(), "the deadline is cleared: no bound on the read, got %v", first)

				return
			}
			assert.False(
				t,
				first.Before(before.Add(tt.timeout)),
				"the deadline is the read timeout from the start of the read",
			)
			assert.False(t, first.After(after.Add(tt.timeout)), "the deadline is not later than the read timeout")
		})
	}
}

// Test_BodyOpts checks the options of the operations that read their own body: no size limit (as on main), the read
// bound by the timeout it is given, and no validation by huma (the handler decodes the body).
func Test_BodyOpts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		timeout     time.Duration
		wantTimeout time.Duration
	}{
		{name: "disabled by the operator", timeout: 0, wantTimeout: -1},
		{name: "the default read timeout", timeout: 30 * time.Second, wantTimeout: 30 * time.Second},
		{name: "a short read timeout", timeout: time.Second, wantTimeout: time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			op := &huma.Operation{}
			controller.BodyOpts(op, tt.timeout)
			assert.Equal(t, tt.wantTimeout, op.BodyReadTimeout)
			assert.Equal(t, int64(-1), op.MaxBodyBytes)
			assert.True(t, op.SkipValidateBody)
		})
	}
}

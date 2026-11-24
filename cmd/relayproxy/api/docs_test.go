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

// swaggerDocument is what the tests read of the OpenAPI document.
type swaggerDocument struct {
	Paths      map[string]map[string]any `json:"paths"`
	Components struct {
		Schemas map[string]any `json:"schemas"`
	} `json:"components"`
	Info struct {
		Version string `json:"version"`
	} `json:"info"`
	Servers []any `json:"servers"`
}

// Test_Swagger checks the documents that the Swagger UI displays. The document describes the whole API (the
// monitoring endpoints included, whether they are served by the same listener or by a dedicated one), carries the
// version of the relay proxy and advertises no server, so that "Try it out" calls the origin of the page. The first
// requests of every document, sent at once, all get the same complete bytes: huma renders a document on its first
// request into a variable it shares, the server renders them before it serves (run with -race).
func Test_Swagger(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		wantPaths      []string
		monitoringPort int
		concurrent     int
	}{
		{
			name:      "one listener",
			wantPaths: []string{"/health", "/info", "/metrics", "/v1/allflags", "/admin/v1/flags/validate"},
		},
		{
			name: "dedicated monitoring listener", monitoringPort: 18080,
			wantPaths: []string{"/health", "/info", "/metrics", "/v1/allflags", "/admin/v1/flags/validate"},
		},
		{name: "first requests", concurrent: 8},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := newDocsServer(t, tt.monitoringPort)
			if tt.concurrent > 0 {
				paths := []string{
					"/swagger/openapi.json", "/swagger/openapi-3.0.json",
					"/swagger/openapi.yaml", "/swagger/openapi-3.0.yaml",
				}
				var (
					wg     sync.WaitGroup
					mu     sync.Mutex
					bodies = map[string][]string{}
				)
				for _, path := range paths {
					for range tt.concurrent {
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

				for _, path := range paths {
					require.Len(t, bodies[path], tt.concurrent)
					if strings.HasSuffix(path, ".json") {
						assert.True(t, json.Valid([]byte(bodies[path][0])), "%s is a complete JSON document", path)
					} else {
						assert.Contains(t, bodies[path][0], "openapi: ", "%s is a complete YAML document", path)
					}
					for _, body := range bodies[path] {
						assert.Equal(t, bodies[path][0], body, path)
					}
				}

				return
			}

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/swagger/openapi.json", nil))
			require.Equal(t, http.StatusOK, rec.Code)
			var doc swaggerDocument
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &doc))
			assert.Equal(t, "v9.9.9", doc.Info.Version)
			assert.Empty(t, doc.Servers)
			for _, path := range tt.wantPaths {
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

// watchedListener hands out connections and keeps the first read deadline the server sets on the first of them:
// before it reads the request, so before any handler (and huma) could set one. It is the read timeout of the server
// itself, the zero time (no deadline) when the server has none.
type watchedListener struct {
	net.Listener

	first      time.Time
	mu         sync.Mutex
	accepted   bool
	firstIsSet bool
}

func (l *watchedListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err //nolint:wrapcheck // transparent
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.accepted {
		return conn, nil
	}
	l.accepted = true

	return &watchedConn{Conn: conn, listener: l}, nil
}

// firstDeadline returns the first read deadline set on the first connection, and whether there was one.
func (l *watchedListener) firstDeadline() (time.Time, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.first, l.firstIsSet
}

type watchedConn struct {
	net.Conn

	listener *watchedListener
}

func (c *watchedConn) SetReadDeadline(t time.Time) error {
	c.listener.mu.Lock()
	if !c.listener.firstIsSet {
		c.listener.first, c.listener.firstIsSet = t, true
	}
	c.listener.mu.Unlock()

	return c.Conn.SetReadDeadline(t) //nolint:wrapcheck // transparent
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

// Test_Upload checks the read of the body on the routes that read their own body (the data collector and the flag
// configuration), through the whole chain of middleware.
//   - The read deadline that the registered routes set is recorded (huma sets it through the writer, following
//     Unwrap): the read timeout of the server counted from the start of the read when it is on, a cleared (zero)
//     deadline when the option disables it, never huma's default of 5 seconds. This is the mechanism, checked in
//     every row without waiting for anything.
//   - When the timeout is on, a stalled upload (the headers announce a body that never arrives in full) is ended by
//     the server end to end: it answers 408 or closes the connection. The pass condition is the outcome: a
//     client-side timeout fails the test, the generous client deadline only keeps a hang from blocking it.
func Test_Upload(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		path         string
		timeout      time.Duration
		wantDeadline bool // a deadline of the length of the timeout is set, else it is cleared
		wantEnded    bool // the stalled upload is ended by the server
	}{
		{name: "collector timeout", path: "/v1/data/collector", timeout: 100 * time.Millisecond,
			wantDeadline: true, wantEnded: true},
		{name: "configuration timeout", path: "/v1/flag/configuration", timeout: 100 * time.Millisecond,
			wantDeadline: true, wantEnded: true},
		{name: "collector no timeout", path: "/v1/data/collector"},
		{name: "configuration no timeout", path: "/v1/flag/configuration"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// the mechanism
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
			// real time on purpose: huma sets the deadline from the real clock, and the network deadlines are enforced
			// by the runtime poller, not by clock.Now.
			before := time.Now()
			srv.Handler().ServeHTTP(rec, req)
			after := time.Now()
			require.NotEmpty(
				t,
				rec.deadlines,
				"the route set no read deadline: the chain hides the connection from huma",
			)
			first := rec.deadlines[0]
			if tt.wantDeadline {
				assert.False(
					t,
					first.Before(before.Add(tt.timeout)),
					"the deadline is the read timeout from the start of the read",
				)
				assert.False(t, first.After(after.Add(tt.timeout)), "the deadline is not later than the read timeout")
			} else {
				assert.True(t, first.IsZero(), "the deadline is cleared: no bound on the read, got %v", first)
			}

			// the listener of the server: its connections get a read deadline only when the server has a read timeout
			// (a request of a client that does not stall is served in both cases)
			var watched *watchedListener
			base := startWrappedServer(t, testConfig(t), func(ln net.Listener) net.Listener {
				watched = &watchedListener{Listener: ln}

				return watched
			}, api.WithReadTimeout(tt.timeout))
			resp, err := http.Post(
				base+tt.path,
				"application/json",
				strings.NewReader(`{"events":[]}`),
			)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			require.Equal(t, http.StatusOK, resp.StatusCode)
			first, set := watched.firstDeadline()
			require.True(t, set, "the server sets a read deadline when it starts to read a request")
			assert.Equal(t, tt.wantDeadline, !first.IsZero(),
				"the server arms its own read deadline (before any handler) only with a read timeout, got %v", first)

			if !tt.wantEnded {
				return
			}

			// the outcome, end to end
			baseURL := startGoldenServer(t, testConfig(t), api.WithReadTimeout(tt.timeout))
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

// TestBodyOpts checks the options of the operations that read their own body: no size limit (as on main), the read
// bound by the timeout it is given, and no validation by huma (the handler decodes the body).
func TestBodyOpts(t *testing.T) {
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

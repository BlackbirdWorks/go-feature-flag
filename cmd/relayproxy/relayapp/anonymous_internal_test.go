package relayapp

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/corehttp"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
)

const (
	anonymousBulkPath = "/ofrep/v1/evaluate/flags"
	adminValidatePath = "/admin/v1/flags/validate"
	infoPath          = "/info"
	bannerText        = "Semaphore Relay Proxy"
	bulkBody          = `{"context":{"targetingKey":"u"}}`
	headerAPIKey      = "X-Api-Key"
	headerContentType = "Content-Type"
	mimeJSON          = "application/json"
)

func jsonHeader() http.Header { return http.Header{headerContentType: {mimeJSON}} }

// applyReload hands the file that is on disk to the relay and waits for the round that applies it.
func (r *relayRig) applyReload(t *testing.T) {
	t.Helper()

	before := r.snapshot
	r.reload <- struct{}{}

	r.awaitRound(func(res flagruntime.RoundResult) bool { return res.Snapshot > before })
}

// pollNext is poll for a relay that already polled: the reader waits for its tick, the first round signal was taken.
func (r *relayRig) pollNext(gen uint64) {
	r.store.t.Helper()

	select {
	case r.sched.release <- struct{}{}:
	case <-time.After(waitBound):
		require.FailNow(r.store.t, "the reader is not waiting for a tick")
	}

	select {
	case <-r.sched.entered:
	case <-time.After(waitBound):
		require.FailNow(r.store.t, "the reader did not end its next round")
	}

	r.awaitGeneration(gen)
}

func (r *relayRig) infoAnonymous(t *testing.T) string {
	t.Helper()

	status, body := get(t, r.url+infoPath, http.Header{"Accept": {mimeJSON}})
	require.Equal(t, http.StatusOK, status)

	_, after, found := strings.Cut(body, `"anonymous":`)
	if !found {
		return ""
	}

	end := strings.Index(after, "}")
	require.GreaterOrEqual(t, end, 0)

	return after[:end+1]
}

// The relay started from a file that names an anonymous flag set serves the requests without a key from it, and only
// those requests: a wrong key is refused, the admin endpoints stay closed. A reload of the file changes it with no
// restart.
func TestRunServesTheAnonymousFlagSet(t *testing.T) {
	t.Parallel()

	rig := startRelay(t, &rigOptions{
		extra: "auth:\n  anonymousFlagSet: tenant\n  adminKeys: [" + adminKey + "]\n",
	})
	rig.awaitGeneration(1)

	status, value := rig.evaluate("")
	assert.Equal(t, http.StatusOK, status, "a request without a key is served")
	assert.InDelta(t, 1, value, 0)

	status, value = rig.evaluate(tenantKey)
	assert.Equal(t, http.StatusOK, status, "a request with the key behaves as before")
	assert.InDelta(t, 1, value, 0)

	status, _ = rig.evaluate("33333333-3333-4333-8333-333333333333")
	assert.Equal(t, http.StatusUnauthorized, status, "a wrong key never falls back to the anonymous flag set")

	status, body := doRequest(t, http.MethodPost, rig.url+anonymousBulkPath, bulkBody, jsonHeader())
	assert.Equal(t, http.StatusOK, status, "the bulk evaluation is served without a key")
	assert.Contains(t, body, `"key":"`+flagKey+`"`)

	status, _ = doRequest(t, http.MethodPost, rig.url+adminValidatePath, "{}", http.Header{})
	assert.Equal(t, http.StatusUnauthorized, status, "an anonymous request never reaches the admin endpoints")

	status, _ = doRequest(t, http.MethodPost, rig.url+adminValidatePath, "{}", http.Header{headerAPIKey: {tenantKey}})
	assert.Equal(t, http.StatusUnauthorized, status, "the key of a flag set is no admin key")

	assert.JSONEq(t, `{"flagSet":"tenant","state":"serving"}`, rig.infoAnonymous(t))

	// a flag set that does not exist: the answer of a key whose flag set is not available, and /info says so
	rig.writeConfig("auth:\n  anonymousFlagSet: ghost\n  adminKeys: [" + adminKey + "]\n")
	rig.applyReload(t)

	status, _ = rig.evaluate("")
	assert.Equal(t, http.StatusNotFound, status, "the flag is not found, as for a flag set that is not available")
	assert.JSONEq(t, `{"flagSet":"ghost","state":"missing"}`, rig.infoAnonymous(t))
	assert.Equal(t, 1, strings.Count(rig.stderr.String(), "the anonymous flag set is not in the generation"))

	status, _ = rig.evaluate(tenantKey)
	assert.Equal(t, http.StatusOK, status, "the keys of the flag sets are not affected")

	// switched off again by a reload, in place
	rig.writeConfig("auth:\n  adminKeys: [" + adminKey + "]\n")
	rig.applyReload(t)

	status, _ = rig.evaluate("")
	assert.Equal(t, http.StatusUnauthorized, status)
	assert.Empty(t, rig.infoAnonymous(t))
}

// By default every request needs a key.
func TestRunWithoutAnonymousFlagSetNeedsAKey(t *testing.T) {
	t.Parallel()

	rig := startRelay(t, &rigOptions{})
	rig.awaitGeneration(1)

	status, _ := rig.evaluate("")
	assert.Equal(t, http.StatusUnauthorized, status)

	status, _ = doRequest(t, http.MethodPost, rig.url+anonymousBulkPath, bulkBody, jsonHeader())
	assert.Equal(t, http.StatusUnauthorized, status)
}

// hideBanner keeps the banner out of the standard output; the default prints it.
func TestRunHideBanner(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		extra      string
		wantBanner bool
	}{
		{name: "the default prints the banner", wantBanner: true},
		{name: "hideBanner keeps it out", extra: "server:\n  hideBanner: true\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rig := startRelay(t, &rigOptions{extra: tt.extra})
			rig.awaitGeneration(1)

			assert.Equal(t, tt.wantBanner, strings.Contains(rig.stdout.String(), bannerText))
		})
	}
}

const opsListeners = "server:\n  listeners:\n    - {name: api, address: \":1031\"}\n" +
	"    - {name: ops, address: \":1032\", role: ops}\n"

// startWithOps starts a relay from a file with an api and an ops listener; the ops listener is open from the start.
func startWithOps(t *testing.T, extra string) (*relayRig, string) {
	t.Helper()

	ops, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ops.Close() })

	rig := startRelay(t, &rigOptions{
		extra:         opsListeners + extra,
		serverOptions: []api.Option{api.WithMonitoringListener(ops)},
	})
	rig.awaitGeneration(1)

	return rig, "http://" + ops.Addr().String()
}

// The profiler is served by the ops listener when the file asks for it, and never by the api listener.
func TestRunServesPprofOnTheOpsListenerOnly(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		extra        string
		wantOps      int
		wantAPI      int
		wantOpsProbe int
	}{
		{
			name: "enabled", extra: "  ops: {pprof: {enabled: true}}\n",
			wantOps: http.StatusOK, wantAPI: http.StatusNotFound, wantOpsProbe: http.StatusOK,
		},
		{
			name:         "off by default",
			wantOps:      http.StatusNotFound,
			wantAPI:      http.StatusNotFound,
			wantOpsProbe: http.StatusOK,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rig, opsURL := startWithOps(t, tt.extra)

			status, _ := get(t, opsURL+"/debug/pprof/cmdline", nil)
			assert.Equal(t, tt.wantOps, status, "the ops listener")

			status, _ = get(t, rig.url+"/debug/pprof/cmdline", nil)
			assert.Equal(t, tt.wantAPI, status, "the api listener never serves it")

			status, _ = get(t, rig.url+"/debug/pprof/", nil)
			assert.Equal(t, http.StatusNotFound, status, "the index of the api listener")

			status, _ = get(t, opsURL+readyPath, nil)
			assert.Equal(t, tt.wantOpsProbe, status, "the probes stay on the ops listener")
		})
	}
}

// The event stream of the file is advertised by the bulk evaluation, and the validate limits cut the bodies.
func TestRunAppliesTheStreamAndValidateSettings(t *testing.T) {
	t.Parallel()

	rig := startRelay(t, &rigOptions{extra: "auth:\n  anonymousFlagSet: tenant\n  adminKeys: [" + adminKey + "]\n" +
		"server:\n  ofrepEventStream: {baseUrl: \"https://flags.example.com\", inactivityDelaySec: 45}\n" +
		"  validateFlags: {maxJsonBodySize: 1KiB}\n"})
	rig.awaitGeneration(1)

	status, body := doRequest(t, http.MethodPost, rig.url+anonymousBulkPath, bulkBody,
		http.Header{headerAPIKey: {tenantKey}, headerContentType: {mimeJSON}})
	assert.Equal(t, http.StatusOK, status)
	assert.Contains(
		t,
		body,
		`"eventStreams":[{"type":"sse","url":"https://flags.example.com/stream/v1/sse/flag/change?apiKey=`+
			tenantKey+`","inactivityDelaySec":45}]`,
	)

	status, body = doRequest(t, http.MethodPost, rig.url+anonymousBulkPath, bulkBody, jsonHeader())
	assert.Equal(t, http.StatusOK, status)
	assert.Contains(
		t,
		body,
		`"eventStreams":[{"type":"sse","url":"https://flags.example.com/stream/v1/sse/flag/change",`+
			`"inactivityDelaySec":45}]`,
		"an anonymous client gets the URL without a key",
	)

	headers := http.Header{headerAPIKey: {adminKey}, headerContentType: {mimeJSON}}

	status, body = doRequest(
		t,
		http.MethodPost,
		rig.url+adminValidatePath,
		`{"x":"`+strings.Repeat("a", 2<<10)+`"}`,
		headers,
	)
	assert.Equal(t, http.StatusRequestEntityTooLarge, status)
	assert.JSONEq(t, `{"message":"request body too large (max 1.0 KiB for json)"}`, body)

	status, _ = doRequest(t, http.MethodPost, rig.url+adminValidatePath, `{"x":1}`, headers)
	assert.NotEqual(t, http.StatusRequestEntityTooLarge, status, "a small body is read")
}

const (
	wsPath  = "/stream/v1/ws/flag/change"
	ssePath = "/stream/v1/sse/flag/change"
)

// streamClient is a client of the change streams: it reads what the relay sends until it is closed.
type streamClient struct {
	events chan string
	closed chan error
	// received counts every message the client got.
	received atomic.Int64
}

func dialWebsocket(t *testing.T, base, key string) *streamClient {
	t.Helper()

	target := "ws" + strings.TrimPrefix(base, "http") + wsPath
	if key != "" {
		target += "?apiKey=" + key
	}

	conn, resp, err := (&websocket.Dialer{}).DialContext(t.Context(), target, nil)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	t.Cleanup(func() { _ = conn.Close() })

	client := &streamClient{events: make(chan string, 16), closed: make(chan error, 1)}

	go func() {
		for {
			_, msg, readErr := conn.ReadMessage()
			if readErr != nil {
				client.closed <- readErr

				return
			}

			client.received.Add(1)

			client.events <- string(msg)
		}
	}()

	return client
}

func dialSSE(t *testing.T, base, key string) *streamClient {
	t.Helper()

	target := base + ssePath
	if key != "" {
		target += "?apiKey=" + key
	}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	t.Cleanup(func() { _ = resp.Body.Close() })

	client := &streamClient{events: make(chan string, 16), closed: make(chan error, 1)}

	go func() {
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			if line := scanner.Text(); strings.HasPrefix(line, "data:") {
				client.received.Add(1)

				client.events <- line
			}
		}

		client.closed <- scanner.Err()
	}()

	return client
}

func (c *streamClient) awaitEvent(t *testing.T, want string) {
	t.Helper()

	select {
	case got := <-c.events:
		assert.Contains(t, got, want)
	case err := <-c.closed:
		require.FailNow(t, "the stream was closed before the change", "%v", err)
	case <-time.After(waitBound):
		require.FailNow(t, "the change did not arrive")
	}
}

// awaitClosed waits for the end of the stream.
func (c *streamClient) awaitClosed(t *testing.T) {
	t.Helper()

	select {
	case closeErr := <-c.closed:
		t.Logf("the stream ended: %v", closeErr)
	case <-time.After(waitBound):
		require.FailNow(t, "the stream was not closed")
	}
}

// A client without a key gets the changes of the anonymous flag set, on the websocket and on the SSE stream. When a
// reload clears the setting, or names another flag set, they are closed at once and get no further change. A client
// with a key of the flag set is never touched.
func TestRunClosesTheAnonymousStreamsWhenTheAccessEnds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// reloaded is the auth block of the file that ends the anonymous access of the flag set tenant.
		reloaded string
	}{
		{name: "the setting is cleared", reloaded: "auth:\n  adminKeys: [" + adminKey + "]\n"},
		{
			name:     "the setting names a flag set that is not held",
			reloaded: "auth:\n  anonymousFlagSet: ghost\n  adminKeys: [" + adminKey + "]\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			offered := make(chan uint64, 16)
			rig := startRelay(t, &rigOptions{
				extra:     "auth:\n  anonymousFlagSet: tenant\n  adminKeys: [" + adminKey + "]\n",
				broadcast: func(change flagruntime.Change) { offered <- change.Generation },
			})
			rig.awaitGeneration(1)

			rel := <-rig.started

			// awaitOffered waits until the core handed the change of the generation to the streams, and until the
			// streams delivered it
			awaitDelivered := func(generation uint64) {
				t.Helper()

				for {
					select {
					case got := <-offered:
						if got < generation {
							continue
						}
					case <-time.After(waitBound):
						require.FailNow(
							t,
							"the core did not hand the change to the streams",
							"generation %d",
							generation,
						)
					}

					break
				}

				require.Eventually(t, rel.streams.Idle, waitBound, time.Millisecond, "the change was delivered")
			}

			// the change of generation 1 reaches no client: they connect after it was delivered
			awaitDelivered(1)

			anonWS, anonSSE := dialWebsocket(t, rig.url, ""), dialSSE(t, rig.url, "")
			keyedWS, keyedSSE := dialWebsocket(t, rig.url, tenantKey), dialSSE(t, rig.url, tenantKey)

			require.Eventually(t, func() bool {
				return rel.streams.Connected() == corehttp.ClientCounts{Websocket: 2, SSE: 2}
			}, waitBound, time.Millisecond, "the four clients are registered")

			rig.store.publish(2)
			rig.poll(2)
			awaitDelivered(2)

			// the message of the websocket carries the new value of the flag: it is the change of generation 2
			anonWS.awaitEvent(t, `"v":2`)
			anonSSE.awaitEvent(t, "refetchEvaluation")
			keyedWS.awaitEvent(t, `"v":2`)
			keyedSSE.awaitEvent(t, "refetchEvaluation")

			rig.writeConfig(tt.reloaded)
			rig.applyReload(t)

			anonWS.awaitClosed(t)
			anonSSE.awaitClosed(t)

			rig.store.publish(3)
			rig.pollNext(3)
			awaitDelivered(3)

			keyedWS.awaitEvent(t, `"v":3`)
			keyedSSE.awaitEvent(t, "refetchEvaluation")

			// exact counts: one message per published generation for the clients with a key, and the one of
			// generation 2 for the clients that were closed before generation 3
			assert.Equal(t, int64(1), anonWS.received.Load(), "messages of the closed websocket client")
			assert.Equal(t, int64(1), anonSSE.received.Load(), "messages of the closed SSE client")
			assert.Equal(t, int64(2), keyedWS.received.Load(), "messages of the websocket client with a key")
			assert.Equal(t, int64(2), keyedSSE.received.Load(), "messages of the SSE client with a key")
			assert.Equal(t, corehttp.ClientCounts{Websocket: 1, SSE: 1}, rel.streams.Connected())
		})
	}
}

const refreshPath = "/admin/v1/retriever/refresh"

// The answer of a refresh is about what the relay serves: the evaluation that follows it already sees the new value.
func TestRunRefreshAnswersWhatIsServed(t *testing.T) {
	t.Parallel()

	rig := startRelay(t, &rigOptions{extra: "auth:\n  adminKeys: [" + adminKey + "]\n"})
	rig.awaitGeneration(1)

	headers := http.Header{headerAPIKey: {adminKey}}

	for _, generation := range []int{2, 3} {
		value := float64(generation)

		rig.store.publish(generation)

		status, body := doRequest(t, http.MethodPost, rig.url+refreshPath, "", headers)
		require.Equal(t, http.StatusOK, status, body)
		assert.JSONEq(t, fmt.Sprintf(
			`{"result":"updated","generation":%d,"refreshed":true,"changed":true,"applied":true,"appliedGeneration":%d}`,
			generation,
			generation,
		), body)

		status, got := rig.evaluate(tenantKey)
		assert.Equal(t, http.StatusOK, status)
		assert.InDelta(t, value, got, 0, "the evaluation after the refresh serves the new generation")
	}
}

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
	"github.com/blackbirdworks/semaphore/controlplane"
)

const (
	anonymousBulkPath = "/ofrep/v1/evaluate/flags"
	adminValidatePath = "/admin/v1/flags/validate"
	infoPath          = "/info"
	bannerText        = "Semaphore Relay Proxy"
	bulkBody          = `{"context":{"targetingKey":"u"}}`
	headerAPIKey      = "X-Api-Key"
	headerAccept      = "Accept"
	headerContentType = "Content-Type"
	mimeJSON          = "application/json"
)

func jsonHeader() http.Header { return http.Header{headerContentType: {mimeJSON}} }

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

// pollRejected lets the reader poll once more and waits for the round in which the engine rejects the generation.
func (r *relayRig) pollRejected(t *testing.T, gen uint64, wantCode string) {
	t.Helper()

	select {
	case r.sched.release <- struct{}{}:
	case <-time.After(waitBound):
		require.FailNow(t, "the reader is not waiting for a tick")
	}

	timeout := time.After(waitBound)

	for {
		select {
		case res := <-r.rounds:
			if res.Outcome == flagruntime.OutcomeRejected && res.Generation == gen {
				assert.Equal(t, wantCode, res.Code)

				return
			}
		case <-timeout:
			require.FailNow(t, "the engine did not reject the generation", r.stderr.String())
		}
	}
}

// infoPublic is the public status of /info.
func (r *relayRig) infoPublic(t *testing.T) string {
	t.Helper()

	status, body := get(t, r.url+infoPath, http.Header{headerAccept: {mimeJSON}})
	require.Equal(t, http.StatusOK, status)

	_, after, found := strings.Cut(body, `"public":`)
	require.True(t, found, "/info has the status of the public flags")

	end := strings.Index(after, "}")
	require.GreaterOrEqual(t, end, 0)

	return after[:end+1]
}

const (
	openSet     = "open"
	openObject  = "flags-open"
	otherObject = "flags-other"
)

// publicSets are the flag sets of the tests: the tenant, with its key, and flag sets without keys.
func publicSets(open ...string) []controlplane.FlagSet {
	sets := make([]controlplane.FlagSet, 0, 1+len(open))
	sets = append(
		sets,
		controlplane.FlagSet{Name: tenantSet, Sources: []string{flagsObject}, APIKeys: []string{tenantKey}},
	)

	for _, name := range open {
		sets = append(sets, controlplane.FlagSet{Name: name, Sources: []string{map[string]string{
			openSet: openObject, "other": otherObject,
		}[name]}})
	}

	return sets
}

// A flag set published without API keys is public: a request without a key is served by it, and only by it. A wrong key
// is refused, the admin endpoints stay closed, and the public flags follow the published generations: withdrawn when
// the last public flag set gets a key.
func TestRunServesThePublicFlags(t *testing.T) {
	t.Parallel()

	rig := startRelay(t, &rigOptions{extra: "auth:\n  adminKeys: [" + adminKey + "]\n"})
	rig.awaitGeneration(1)

	status, _ := rig.evaluate("")
	assert.Equal(t, http.StatusUnauthorized, status, "no flag set is public yet: a request without a key is refused")
	assert.JSONEq(t, `{"sets":[],"flags":0,"present":false}`, rig.infoPublic(t))

	// generation 2 publishes a flag set without keys
	rig.store.publishDocs(
		publicSets(openSet),
		map[string]string{flagsObject: flagYAML(1), openObject: flagYAML(100)},
	)
	rig.poll()

	status, value := rig.evaluate("")
	assert.Equal(t, http.StatusOK, status, "a request without a key is served")
	assert.InDelta(t, 100, value, 0, "by the public flags")

	status, value = rig.evaluate(tenantKey)
	assert.Equal(t, http.StatusOK, status, "a request with the key behaves as before")
	assert.InDelta(t, 1, value, 0, "and gets the flags of its flag set")

	status, _ = rig.evaluate("33333333-3333-4333-8333-333333333333")
	assert.Equal(t, http.StatusUnauthorized, status, "a wrong key never falls back to the public flags")

	status, body := doRequest(t, http.MethodPost, rig.url+anonymousBulkPath, bulkBody, jsonHeader())
	assert.Equal(t, http.StatusOK, status, "the bulk evaluation is served without a key")
	assert.Contains(t, body, `"key":"`+flagKey+`"`)

	status, _ = doRequest(t, http.MethodPost, rig.url+adminValidatePath, "{}", http.Header{})
	assert.Equal(t, http.StatusUnauthorized, status, "a request without a key never reaches the admin endpoints")

	status, _ = doRequest(t, http.MethodPost, rig.url+adminValidatePath, "{}", http.Header{headerAPIKey: {tenantKey}})
	assert.Equal(t, http.StatusUnauthorized, status, "the key of a flag set is no admin key")

	assert.JSONEq(t, `{"sets":["open"],"flags":1,"present":true}`, rig.infoPublic(t))

	// generation 3 changes the public flags: no restart, no reload of the file
	rig.store.publishDocs(
		publicSets(openSet),
		map[string]string{flagsObject: flagYAML(1), openObject: flagYAML(101)},
	)
	rig.pollNext(3)

	status, value = rig.evaluate("")
	assert.Equal(t, http.StatusOK, status)
	assert.InDelta(t, 101, value, 0)

	// generation 4 gives the flag set a key: nothing is public any more
	rig.store.publishDocs([]controlplane.FlagSet{
		{Name: tenantSet, Sources: []string{flagsObject}, APIKeys: []string{tenantKey}},
		{Name: openSet, Sources: []string{openObject}, APIKeys: []string{"55555555-5555-4555-8555-555555555555"}},
	}, map[string]string{flagsObject: flagYAML(1), openObject: flagYAML(101)})
	rig.pollNext(4)

	status, _ = rig.evaluate("")
	assert.Equal(t, http.StatusUnauthorized, status, "the flags are not public any more")
	assert.JSONEq(t, `{"sets":[],"flags":0,"present":false}`, rig.infoPublic(t))
}

// Two flag sets published without keys form one namespace: a flag key that both define is refused. The generation is
// rejected whole and the relay keeps the last known good, public flags included.
func TestRunRejectsAFlagKeyInTwoPublicFlagSets(t *testing.T) {
	t.Parallel()

	rig := startRelay(t, &rigOptions{extra: "auth:\n  adminKeys: [" + adminKey + "]\n"})
	rig.awaitGeneration(1)

	rig.store.publishDocs(
		publicSets(openSet),
		map[string]string{flagsObject: flagYAML(1), openObject: flagYAML(100)},
	)
	rig.poll()

	status, value := rig.evaluate("")
	require.Equal(t, http.StatusOK, status)
	assert.InDelta(t, 100, value, 0)

	// generation 3: a second public flag set defines the flag f too
	rig.store.publishDocs(publicSets(openSet, "other"), map[string]string{
		flagsObject: flagYAML(1), openObject: flagYAML(101), otherObject: flagYAML(5),
	})

	rig.pollRejected(t, 3, flagruntime.CodeBuildFailed)

	status, value = rig.evaluate("")
	assert.Equal(t, http.StatusOK, status, "the relay keeps serving the last known good")
	assert.InDelta(t, 100, value, 0)
	assert.Contains(t, rig.stderr.String(), "defined in open and other")
	assert.NotContains(t, rig.stderr.String(), tenantKey)
}

// With no flag set published without keys, every request needs a key.
func TestRunWithoutPublicFlagsNeedsAKey(t *testing.T) {
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

	rig := startRelay(t, &rigOptions{extra: "auth:\n  adminKeys: [" + adminKey + "]\n" +
		"server:\n  ofrepEventStream: {baseUrl: \"https://flags.example.com\", inactivityDelaySec: 45}\n" +
		"  validateFlags: {maxJsonBodySize: 1KiB}\n"})
	rig.awaitGeneration(1)

	// a flag set without keys is public: a client without a key is served
	rig.store.publishDocs(publicSets(openSet), map[string]string{
		flagsObject: flagYAML(1), openObject: flagYAML(100),
	})
	rig.poll()

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

// A client without a key gets the changes of the public flags, on the websocket and on the SSE stream, as a client with
// a key gets the ones of its flag set. When a generation withdraws the public flags (their flag set gets a key) the
// clients without a key are closed at once and get nothing more. A client with a key is never touched.
func TestRunClosesThePublicStreamsWhenTheFlagsAreWithdrawn(t *testing.T) {
	t.Parallel()

	// the changes of the generations 1 and 2 are held back by the hook until the four clients are registered: they all
	// get theirs, and the counts below are exact
	held := make(chan struct{})
	rig := startRelay(t, &rigOptions{
		extra: "auth:\n  adminKeys: [" + adminKey + "]\n",
		broadcast: func(change flagruntime.Change) {
			if change.Generation <= 2 {
				<-held
			}
		},
	})
	t.Cleanup(func() {
		select {
		case <-held:
		default:
			close(held)
		}
	})

	rig.awaitGeneration(1)

	rel := <-rig.started

	rig.store.publishDocs(publicSets(openSet), map[string]string{
		flagsObject: flagYAML(1), openObject: flagYAML(100),
	})
	rig.poll()

	keylessWS, keylessSSE := dialWebsocket(t, rig.url, ""), dialSSE(t, rig.url, "")
	keyedWS, keyedSSE := dialWebsocket(t, rig.url, tenantKey), dialSSE(t, rig.url, tenantKey)

	require.Eventually(t, func() bool {
		return rel.streams.Connected() == corehttp.ClientCounts{Websocket: 2, SSE: 2}
	}, waitBound, time.Millisecond, "the four clients are registered")

	close(held)

	// generation 1: the flag set with the key; generation 2: the public flags
	keyedWS.awaitEvent(t, `"v":1`)
	keyedSSE.awaitEvent(t, "refetchEvaluation")
	keylessWS.awaitEvent(t, `"v":100`)
	keylessSSE.awaitEvent(t, "refetchEvaluation")

	// generation 3: the public flags change, nothing else does
	rig.store.publishDocs(publicSets(openSet), map[string]string{
		flagsObject: flagYAML(1), openObject: flagYAML(101),
	})
	rig.pollNext(3)

	keylessWS.awaitEvent(t, `"v":101`)
	keylessSSE.awaitEvent(t, "refetchEvaluation")

	// generation 4: the public flag set gets a key, and the flag set of the key changes
	rig.store.publishDocs([]controlplane.FlagSet{
		{Name: tenantSet, Sources: []string{flagsObject}, APIKeys: []string{tenantKey}},
		{Name: openSet, Sources: []string{openObject}, APIKeys: []string{"55555555-5555-4555-8555-555555555555"}},
	}, map[string]string{flagsObject: flagYAML(2), openObject: flagYAML(101)})
	rig.pollNext(4)

	keylessWS.awaitClosed(t)
	keylessSSE.awaitClosed(t)
	keyedWS.awaitEvent(t, `"v":2`)
	keyedSSE.awaitEvent(t, "refetchEvaluation")

	// exact counts: the clients without a key got the changes of the generations 2 and 3, the ones with a key those
	// of the generations 1 and 4
	assert.Equal(t, int64(2), keylessWS.received.Load(), "messages of the websocket client without a key")
	assert.Equal(t, int64(2), keylessSSE.received.Load(), "messages of the SSE client without a key")
	assert.Equal(t, int64(2), keyedWS.received.Load(), "messages of the websocket client with a key")
	assert.Equal(t, int64(2), keyedSSE.received.Load(), "messages of the SSE client with a key")
	assert.Equal(t, corehttp.ClientCounts{Websocket: 1, SSE: 1}, rel.streams.Connected())
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

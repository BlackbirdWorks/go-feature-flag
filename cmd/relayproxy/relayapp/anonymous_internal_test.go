package relayapp

import (
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
)

const (
	anonymousBulkPath = "/ofrep/v1/evaluate/flags"
	adminValidatePath = "/admin/v1/flags/validate"
	infoPath          = "/info"
	bannerText        = "Semaphore Relay Proxy"
	bulkBody          = `{"context":{"targetingKey":"u"}}`
)

func jsonHeader() http.Header { return http.Header{"Content-Type": {"application/json"}} }

// applyReload hands the file that is on disk to the relay and waits for the round that applies it.
func (r *relayRig) applyReload(t *testing.T) {
	t.Helper()

	before := r.snapshot
	r.reload <- struct{}{}
	r.awaitRound(func(res flagruntime.RoundResult) bool { return res.Snapshot > before })
}

func (r *relayRig) infoAnonymous(t *testing.T) string {
	t.Helper()

	status, body := get(t, r.url+infoPath, nil)
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

	status, _ = doRequest(t, http.MethodPost, rig.url+adminValidatePath, "{}", http.Header{"X-Api-Key": {tenantKey}})
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
func startWithOps(t *testing.T, extra string) (rig *relayRig, opsURL string) {
	t.Helper()

	ops, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ops.Close() })

	rig = startRelay(t, &rigOptions{
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
		{name: "off by default", wantOps: http.StatusNotFound, wantAPI: http.StatusNotFound, wantOpsProbe: http.StatusOK},
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
		http.Header{"X-Api-Key": {tenantKey}, "Content-Type": {"application/json"}})
	assert.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, `"eventStreams":[{"type":"sse","url":"https://flags.example.com/stream/v1/sse/flag/change?apiKey=`+
		tenantKey+`","inactivityDelaySec":45}]`)

	status, body = doRequest(t, http.MethodPost, rig.url+anonymousBulkPath, bulkBody, jsonHeader())
	assert.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, `"eventStreams":[{"type":"sse","url":"https://flags.example.com/stream/v1/sse/flag/change",`+
		`"inactivityDelaySec":45}]`, "an anonymous client gets the URL without a key")

	headers := http.Header{"X-Api-Key": {adminKey}, "Content-Type": {"application/json"}}

	status, body = doRequest(t, http.MethodPost, rig.url+adminValidatePath, `{"x":"`+strings.Repeat("a", 2<<10)+`"}`, headers)
	assert.Equal(t, http.StatusRequestEntityTooLarge, status)
	assert.JSONEq(t, `{"message":"request body too large (max 1.0 KiB for json)"}`, body)

	status, _ = doRequest(t, http.MethodPost, rig.url+adminValidatePath, `{"x":1}`, headers)
	assert.NotEqual(t, http.StatusRequestEntityTooLarge, status, "a small body is read")
}

//go:build docker

package relaycore_test

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/internal/connection"
	"github.com/blackbirdworks/semaphore/testhelpers/gopherstack"
)

// countingWire is the transport under the SDK of the S3 connection: it records the method, the key and the status of
// every request handed to it.
type countingWire struct {
	seen []string
	mu   sync.Mutex
}

func (w *countingWire) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := http.DefaultTransport.RoundTrip(req)

	status := 0
	if resp != nil {
		status = resp.StatusCode
	}

	w.mu.Lock()
	w.seen = append(w.seen, fmt.Sprintf("%s %s %d", req.Method, req.URL.Path, status))
	w.mu.Unlock()

	if err != nil {
		return nil, fmt.Errorf("counting wire: %w", err)
	}

	return resp, nil
}

// take returns the requests since the last call.
func (w *countingWire) take() []string {
	w.mu.Lock()
	defer w.mu.Unlock()

	out := w.seen
	w.seen = nil

	return out
}

// requestCounts is what a trigger cost the store.
type requestCounts struct {
	// Total is every request, Manifest the GETs of the manifest, NotModified those answered 304, Objects the GETs of
	// any other key.
	Total, Manifest, NotModified, Objects int
}

func countRequests(reqs []string) requestCounts {
	counts := requestCounts{Total: len(reqs)}

	for _, req := range reqs {
		switch {
		case !strings.HasPrefix(req, "GET "):
		case !strings.Contains(req, manifestKey):
			counts.Objects++
		case strings.HasSuffix(req, " 304"):
			counts.Manifest++
			counts.NotModified++
		default:
			counts.Manifest++
		}
	}

	return counts
}

// awaitRound waits for the next round of the engine and returns it.
func (r *rig) awaitRound(t *testing.T) flagruntime.RoundResult {
	t.Helper()

	select {
	case res := <-r.rounds:
		return res
	case <-t.Context().Done():
		require.FailNow(t, "the test ended before a round")
	}

	return flagruntime.RoundResult{}
}

// A reload that leaves the control plane unchanged reads no object, whatever starts it. The numbers are asserted per
// trigger: the bootstrap reload (SIGHUP) makes no request at all, the refresh of the control plane
// (POST /admin/v1/retriever/refresh) and the scheduled poll make exactly one conditional GET of the manifest,
// answered 304, and never an object GET. There is no config reload endpoint: the triggers are these three.
func TestE2E_UnchangedReloadMakesNoObjectRequest(t *testing.T) {
	t.Parallel()

	srv := gopherstack.Connect(t)
	bucket := srv.CreateBucket(t, "relaycore-reqs")
	wire := &countingWire{}

	doc := "version: 2\nconnections:\n  " + connName + ":\n    type: s3\n    endpoint: " + srv.Endpoint +
		"\n    region: " + gopherstack.Region + "\n    bucket: " + bucket + "\n    pathStyle: true\n" +
		"    credentials:\n      type: static\n      accessKeyId: {fromEnv: AK}\n" +
		"      secretAccessKey: {fromEnv: SK}\n" + controlDoc(connName, "")

	hookURL, _ := hookServer(t)
	env := map[string]string{"AK": gopherstack.AccessKey, "SK": gopherstack.SecretKey, hookEnv: hookURL}
	opts := secretOptions(env)

	pub := twoSets(t, func(key string, data []byte) { srv.PutObject(t, bucket, key, data) })
	pub.publish()

	rdeps, harness := newDeps(t, opts)
	rdeps.Connection = append(rdeps.Connection, connection.WithRoundTripper(wire))

	harness.start(t, parse(t, doc, opts), rdeps)
	harness.settle(t)
	harness.awaitGeneration(t, 1)

	cold := countRequests(wire.take())
	assert.Equal(t, 3, cold.Objects, "the cold start reads the sets document and the two flags objects once")
	assert.Equal(t, 1, cold.Manifest, "and the manifest once")

	// each trigger leaves the control plane as it was: the runtime is not replaced and the cost is the want
	trigger := func(trigger func(), want requestCounts) {
		before := harness.core.Holder().Load()

		trigger()

		assert.Equal(t, want, countRequests(wire.take()))
		assert.Same(t, before, harness.core.Holder().Load(), "no new runtime")
	}

	trigger(func() {
		require.NoError(t, harness.core.Reload(parse(t, doc, opts)))
		assert.Equal(t, flagruntime.OutcomeIgnored, harness.awaitRound(t).Outcome)
	}, requestCounts{})

	trigger(func() {
		res, err := harness.core.Refresh(t.Context())
		require.NoError(t, err)
		assert.Equal(t, uint64(1), res.Running)

		harness.settle(t) // the refresh is a round of the reader: it ends by waiting for its next tick
	}, requestCounts{Total: 1, Manifest: 1, NotModified: 1})

	trigger(func() { harness.poll(t) }, requestCounts{Total: 1, Manifest: 1, NotModified: 1})
}

//go:build docker

package events_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/events"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/internal/connection"
	"github.com/blackbirdworks/semaphore/testhelpers/gopherstack"
)

// countingTransport counts the requests that go through the client of a connection.
type countingTransport struct {
	next  http.RoundTripper
	count atomic.Int64
}

func (c *countingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	c.count.Add(1)

	return c.next.RoundTrip(req)
}

// TestE2E_S3ExporterThroughTheDispatcher builds an S3 exporter from a v2 bootstrap document and the connection
// factory, sends events through the dispatcher, and reads the file it wrote from an isolated bucket. The
// counting round tripper of the factory proves that the exporter used the client of the connection.
func TestE2E_S3ExporterThroughTheDispatcher(t *testing.T) {
	t.Parallel()

	const (
		collectors = 4
		perCollect = 25
		path       = "events/e2e"
	)

	srv := gopherstack.Connect(t)
	bucket := srv.CreateBucket(t, "events")

	doc := fmt.Sprintf(`exporters:
  archive: {kind: s3, connection: ev, path: %s, format: json, filename: events.json, flushInterval: 1h}
`, path)
	cfg, opts := parseDoc(t, fmt.Sprintf(`version: 2
connections:
  ev: {type: s3, region: %s, bucket: %s, endpoint: "%s", pathStyle: true,
       credentials: {type: static, accessKeyId: {fromEnv: AK}, secretAccessKey: {fromEnv: SK}}}
`, gopherstack.Region, bucket, srv.Endpoint)+doc,
		map[string]string{"AK": gopherstack.AccessKey, "SK": gopherstack.SecretKey})

	transport := &countingTransport{next: http.DefaultTransport}
	fac := connectionFactory(t, cfg, opts, connection.WithRoundTripper(transport))

	runtime := events.NewRuntime(&events.RuntimeConfig{Builder: events.Builder{Factory: fac, Secrets: opts}})
	require.NoError(t, runtime.Start(context.Background(), nil))

	report, err := runtime.Reload(context.Background(), events.SpecsFrom(cfg))
	require.NoError(t, err)
	require.Equal(t, []string{"archive"}, report.Exporters.Added)

	var wg sync.WaitGroup

	for c := range collectors {
		wg.Go(func() {
			for i := range perCollect {
				assert.True(t, runtime.Collect(ev(c*perCollect+i)))
			}
		})
	}

	wg.Wait()

	shutdown, err := runtime.Shutdown(bounded(t))
	require.NoError(t, err)
	assert.Zero(t, shutdown.Undelivered)

	keys := srv.ListKeys(t, bucket, path)
	require.Equal(t, []string{path + "/events.json"}, keys, "one file for the one flush")

	got := map[string]int{}
	scanner := bufio.NewScanner(bytes.NewReader(srv.GetObject(t, bucket, keys[0])))

	for scanner.Scan() {
		var event struct {
			Key string `json:"key"`
		}

		require.NoError(t, json.Unmarshal(scanner.Bytes(), &event))

		got[event.Key]++
	}

	require.NoError(t, scanner.Err())

	wantKeys := eventKeys(collectors * perCollect)
	gotKeys := make([]string, 0, len(got))

	for key := range got {
		gotKeys = append(gotKeys, key)
	}

	slices.Sort(gotKeys)
	assert.Equal(t, wantKeys, gotKeys)

	for key, n := range got {
		assert.Equal(t, 1, n, "event %s is in the file once", key)
	}

	assert.Positive(t, transport.count.Load(), "the upload went through the client of the connection")
	assert.Equal(t, map[string]int{"ev": 1}, fac.Builds(), "one configuration for the connection")

	status := statusOf(t, new(runtime.Status()), "archive")
	assert.Equal(t, uint64(collectors*perCollect), status.Exported)
	assert.Equal(t, events.ErrorNone, status.LastError)
}

// TestE2E_WebhookExporterAndNotifier sends events to a webhook exporter and a change to a webhook notifier, both
// built from a bootstrap document, against test servers.
func TestE2E_WebhookExporterAndNotifier(t *testing.T) {
	t.Parallel()

	type request struct {
		header http.Header
		body   []byte
	}

	var (
		mu       sync.Mutex
		exported []request
		notified []request
	)

	record := func(into *[]request) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(r.Body)
			assert.NoError(t, err)

			mu.Lock()
			defer mu.Unlock()

			*into = append(*into, request{header: r.Header.Clone(), body: body})

			w.WriteHeader(http.StatusOK)
		}
	}

	exportServer := httptest.NewServer(record(&exported))
	t.Cleanup(exportServer.Close)

	notifyServer := httptest.NewServer(record(&notified))
	t.Cleanup(notifyServer.Close)

	cfg, opts := parseDoc(t, `version: 2
exporters:
  hook: {kind: webhook, endpointUrl: {fromEnv: EXPORT_URL}, secret: {fromEnv: SIGN}, flushInterval: 1h,
         headers: {X-Relay: {fromEnv: RELAY}}}
notifiers:
  changes: {kind: webhook, endpointUrl: {fromEnv: NOTIFY_URL}, headers: {X-Relay: {fromEnv: RELAY}}}
`, map[string]string{
		"EXPORT_URL": exportServer.URL, "NOTIFY_URL": notifyServer.URL, "SIGN": "signing-key", "RELAY": "relay-1",
	})

	runtime := events.NewRuntime(&events.RuntimeConfig{Builder: events.Builder{Secrets: opts}})
	changes := make(chan flagruntime.Change)

	require.NoError(t, runtime.Start(context.Background(), changes))

	_, err := runtime.Reload(context.Background(), events.SpecsFrom(cfg))
	require.NoError(t, err)

	for i := range 5 {
		require.True(t, runtime.Collect(ev(i)))
	}

	changes <- change("checkout", 7)

	_, err = runtime.Shutdown(bounded(t))
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()

	require.Len(t, exported, 1, "one flush at the shutdown")
	assert.Equal(t, "relay-1", exported[0].header.Get("X-Relay"))
	assert.NotEmpty(t, exported[0].header.Get("X-Hub-Signature-256"), "the body is signed with the secret")

	var payload struct {
		Events []struct {
			Key string `json:"key"`
		} `json:"events"`
	}

	require.NoError(t, json.Unmarshal(exported[0].body, &payload))
	assert.Len(t, payload.Events, 5)

	require.Len(t, notified, 1, "the change reached the notifier")
	assert.Equal(t, "relay-1", notified[0].header.Get("X-Relay"))
	assert.Contains(t, string(notified[0].body), `"added"`)
	assert.Contains(t, string(notified[0].body), `"7"`)
}

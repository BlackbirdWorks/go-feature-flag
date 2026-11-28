package publish_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/cli/helper"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/corehttp"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/relaycore"
	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/controlplane/filepublish"
	"github.com/blackbirdworks/semaphore/controlplane/s3publish"
)

const (
	fileConn   = "control"
	fileAPIKey = "8d3b5f0e-6a4f-4c8e-9c3e-2f2f5d1a7b10"
	fileWait   = 30 * time.Second
	fileTick   = 50 * time.Millisecond
)

// relayOn starts the relay core on the bootstrap file of the rig, with its own polling loop, and serves its HTTP
// handler. It returns the base URL.
func (r *fileRig) relayOn(t *testing.T) string {
	t.Helper()

	raw, err := os.ReadFile(r.config)
	require.NoError(t, err)

	cfg, err := bootstrap.Parse(raw, nil, bootstrap.LoadOptions{})
	require.NoError(t, err)

	metrics, err := metric.NewMetrics()
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())

	core, err := relaycore.New(ctx, cfg, &relaycore.Deps{Registerer: metrics.Registry})
	require.NoError(t, err)

	streams := corehttp.NewStreams(ctx)
	done := make(chan error, 1)

	go func() { done <- core.Run(ctx) }()

	t.Cleanup(func() {
		cancel()

		stop, stopCancel := context.WithTimeout(context.Background(), fileWait)
		defer stopCancel()

		assert.NoError(t, core.Shutdown(stop))
		assert.NoError(t, streams.Close(stop))
		assert.NoError(t, <-done)
	})

	srv, err := corehttp.New(core, &corehttp.Config{
		Settings: &api.Settings{}, Streams: streams, Metrics: metrics, Readiness: corehttp.NewReadiness(core),
	})
	require.NoError(t, err)

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	return ts.URL
}

// evaluate returns the value of the flag checkout-new, or an error while the relay does not serve it yet.
func evaluate(ctx context.Context, base string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/ofrep/v1/evaluate/flags/checkout-new",
		strings.NewReader(`{"context": {"targetingKey": "user-1"}}`))
	if err != nil {
		return false, fmt.Errorf("request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", fileAPIKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("evaluate: %w", err)
	}

	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, fmt.Errorf("read: %w", err)
	}

	var out struct {
		Value bool `json:"value"`
	}

	err = json.Unmarshal(body, &out)
	if err != nil || resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("%w: %d %s", errNotServed, resp.StatusCode, body)
	}

	return out.Value, nil
}

var errNotServed = errors.New("the flag is not served")

// awaitValue waits until the relay evaluates checkout-new to want.
func awaitValue(t *testing.T, base string, want bool) {
	t.Helper()

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		got, err := evaluate(t.Context(), base)
		if assert.NoError(c, err) {
			assert.Equal(c, want, got)
		}
	}, fileWait, fileTick)
}

func flagsDefaulting(variation string) string {
	return strings.Replace(fileFlagsDoc, `variation: "off"`, `variation: "`+variation+`"`, 1)
}

func TestFileStorePublishThenRelayServesAndFollowsAChange(t *testing.T) {
	t.Parallel()

	rig := newFileRig(t)

	out, code := rig.run(t, rig.src, "--config", rig.config)
	require.Zero(t, code, out)

	base := rig.relayOn(t)
	awaitValue(t, base, false)

	rig.writeFlags(t, flagsDefaulting("on"))

	out, code = rig.run(t, rig.src, "--config", rig.config)
	require.Zero(t, code, out)
	assert.Contains(t, out, "published generation 2 (was 1)")

	awaitValue(t, base, true)
}

func TestFileStoreConcurrentPublishesNeverCorruptTheStore(t *testing.T) {
	t.Parallel()

	rig := newFileRig(t)

	_, code := rig.run(t, rig.src, "--config", rig.config)
	require.Zero(t, code)

	// two sources that differ from the published one and from each other
	sources := make([]*fileRig, 2)

	for i := range sources {
		sources[i] = &fileRig{src: filepath.Join(t.TempDir(), "src"), store: rig.store, config: rig.config}
		sources[i].write(
			t,
			"flags/checkout.yaml",
			strings.Replace(fileFlagsDoc, "checkout-new", fmt.Sprintf("checkout-%d", i), 1),
		)
		sources[i].write(t, "flagsets/web.yaml", fileFlagSetDoc)
	}

	codes := make([]int, len(sources))

	var wg sync.WaitGroup

	for i, source := range sources {
		wg.Go(func() { _, codes[i] = source.run(t, source.src, "--config", rig.config) })
	}

	wg.Wait()

	wins := 0

	for _, c := range codes {
		switch c {
		case 0:
			wins++
		default:
			assert.Equal(t, helper.ExitConflict, c, "a publisher that did not win gets the conflict code")
		}
	}

	require.GreaterOrEqual(t, wins, 1, "one publisher always wins")

	manifest, err := os.ReadFile(filepath.Join(rig.store, "manifests", "stable.yaml"))
	require.NoError(t, err)
	assert.Contains(t, string(manifest), fmt.Sprintf("generation: %d", 1+wins))

	out, vcode := rig.run(t, "verify", "--config", rig.config)
	assert.Zero(t, vcode, out)
}

// failManifest fails the write of the manifest: the publisher was killed after the objects.
type failManifest struct {
	s3publish.API

	key string
}

var errKilled = errors.New("killed before the manifest")

func (f *failManifest) PutObject(
	ctx context.Context,
	input *s3.PutObjectInput,
	opts ...func(*s3.Options),
) (*s3.PutObjectOutput, error) {
	if aws.ToString(input.Key) == f.key {
		return nil, errKilled
	}

	out, err := f.API.PutObject(ctx, input, opts...)
	if err != nil {
		return nil, fmt.Errorf("put: %w", err)
	}

	return out, nil
}

func TestFileStoreAPublishKilledBeforeTheManifestLeavesThePreviousGeneration(t *testing.T) {
	t.Parallel()

	rig := newFileRig(t)

	out, code := rig.run(t, rig.src, "--config", rig.config)
	require.Zero(t, code, out)

	base := rig.relayOn(t)
	awaitValue(t, base, false)

	manifestPath := filepath.Join(rig.store, "manifests", "stable.yaml")
	before, err := os.ReadFile(manifestPath)
	require.NoError(t, err)

	store, err := filepublish.New(rig.store)
	require.NoError(t, err)

	pub, err := s3publish.New(&failManifest{API: store, key: "manifests/stable.yaml"},
		&s3publish.Config{Bucket: rig.store, Channel: "stable", Connection: fileConn})
	require.NoError(t, err)

	body, err := controlplane.MarshalFlags(controlplane.Flags{
		"checkout-new": {
			Variations:  map[string]any{"on": true, "off": false},
			DefaultRule: &controlplane.Rule{Variation: new("on")},
		},
	})
	require.NoError(t, err)

	_, err = pub.Publish(t.Context(), s3publish.Request{Objects: []s3publish.Object{
		{Name: "checkout", Kind: controlplane.KindFlags, Format: controlplane.FormatYAML, Body: body},
		{Name: "web", Kind: controlplane.KindFlagSets, Format: controlplane.FormatYAML, Body: []byte(fileFlagSetDoc)},
	}})
	require.ErrorIs(t, err, errKilled)

	after, err := os.ReadFile(manifestPath)
	require.NoError(t, err)
	assert.True(t, bytes.Equal(before, after), "the manifest is untouched")

	// the relay stays on the previous generation, through at least one more poll of its
	require.Never(t, func() bool {
		got, evalErr := evaluate(t.Context(), base)

		return evalErr != nil || got
	}, 3*time.Second, fileTick)

	// the objects of the killed run are on disk but unreferenced: the next publish reuses them and goes live
	rig.writeFlags(t, flagsDefaulting("on"))

	out, code = rig.run(t, rig.src, "--config", rig.config)
	require.Zero(t, code, out)
	assert.Contains(t, out, "published generation 2 (was 1)")

	awaitValue(t, base, true)
}

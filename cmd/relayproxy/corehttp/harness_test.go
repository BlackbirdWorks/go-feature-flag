package corehttp_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/corehttp"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/events"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/relaycore"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/cmdhelpers/retrieverconf"
	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/internal/manifestreader"
)

const (
	// waitBound bounds every wait of the tests; it is a failure bound, never a pause.
	waitBound   = 30 * time.Second
	tenantKey   = "tenant-key"
	adminKey    = "admin-key"
	connName    = "control"
	channelName = "stable"
	manifestKey = "manifests/stable.yaml"
	flagsObject = "flags-a"
	setsObject  = "sets"
	setName     = "tenant"
)

// flagsYAML is the flags of the equivalence tests: one of each type, a rule, a bucket, a disabled flag, a flag with a
// scheduled rollout that is over and one that is not started (the clock is frozen at 2025-06-15).
const flagsYAML = `
bool-flag:
  variations: {enabled: true, disabled: false}
  targeting:
    - {name: vip, query: 'tier eq "vip"', variation: enabled}
  defaultRule: {name: default, variation: disabled}
  metadata: {team: core}
  trackEvents: true
string-flag:
  variations: {a: alpha, b: beta}
  defaultRule: {name: default, variation: a}
untracked-flag:
  variations: {a: alpha, b: beta}
  defaultRule: {name: default, variation: a}
  trackEvents: false
number-flag:
  variations: {low: 1, high: 42}
  targeting:
    - {name: big, query: 'size gt 10', variation: high}
  defaultRule: {name: default, variation: low}
json-flag:
  variations:
    on: {name: on, items: [1, 2], nested: {deep: true}}
    off: {name: off}
  defaultRule: {name: default, variation: on}
disabled-flag:
  disable: true
  variations: {a: 1, b: 2}
  defaultRule: {name: default, variation: a}
bucket-flag:
  variations: {a: a, b: b}
  defaultRule:
    name: default
    percentage: {a: 50, b: 50}
scheduled-over-flag:
  variations: {old: old, new: new}
  defaultRule: {name: default, variation: old}
  scheduledRollout:
    - date: 2022-01-01T00:00:00Z
      defaultRule: {name: default, variation: new}
scheduled-future-flag:
  variations: {old: old, new: new}
  defaultRule: {name: default, variation: old}
  scheduledRollout:
    - date: 2030-01-01T00:00:00Z
      defaultRule: {name: default, variation: new}
`

// serve starts a server on the handler of the API server; the listener is open from the start.
func serve(t testing.TB, srv *api.Server) string {
	t.Helper()

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	return ts.URL
}

// newV1Manager is the v1 flag set manager of flagsYAML, with a file retriever.
func newV1Manager(t testing.TB) (*config.Config, *service.FlagsetManagerImpl) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "flags.yaml")
	require.NoError(t, os.WriteFile(path, []byte(flagsYAML), 0o600))

	conf := &config.Config{
		FlagSets: []config.FlagSet{{
			Name: setName, APIKeys: []string{tenantKey},
			Retriever: &retrieverconf.RetrieverConf{Kind: retrieverconf.FileRetriever, Path: path},
		}},
		AuthorizedKeys: config.APIKeys{Admin: []string{adminKey}},
	}
	conf.ForceReloadAPIKeys()

	manager, err := service.NewFlagsetManager(conf, slog.New(slog.DiscardHandler), nil, nil, nil)
	require.NoError(t, err)
	t.Cleanup(manager.Close)

	return conf, manager
}

// newV1 serves flagsYAML from the v1 relay behind the same handlers.
func newV1(t testing.TB) string {
	t.Helper()

	conf, manager := newV1Manager(t)
	logger := slog.New(slog.DiscardHandler)

	metrics, err := metric.NewMetrics()
	require.NoError(t, err)

	srv := api.New(&api.Settings{}, service.NewAPIKeys(conf), &service.Services{
		MonitoringService: service.NewMonitoring(manager),
		FlagsetManager:    manager,
		Metrics:           metrics,
	}, logger)

	return serve(t, &srv)
}

// tickScheduler is the scheduler of the reader that the test drives: a round per tick, no timer.
type tickScheduler struct {
	entered chan struct{}
	release chan struct{}
}

func (s *tickScheduler) Wait(ctx context.Context, _ time.Duration) error {
	s.entered <- struct{}{}

	select {
	case <-ctx.Done():
		return fmt.Errorf("tick scheduler: %w", ctx.Err())
	case <-s.release:
		return nil
	}
}

// recorder is the exporter manager of the tests: it keeps the events it is given.
type recorder struct {
	got chan *exporter.FeatureEvent
}

func (r *recorder) AddEvent(event *exporter.FeatureEvent) { r.got <- event }
func (*recorder) Start()                                  {}
func (*recorder) Stop()                                   {}

// v2 is a core on a file store, driven by the test, and the HTTP server on it.
type v2 struct {
	core    *relaycore.Core
	sched   *tickScheduler
	rounds  chan flagruntime.RoundResult
	events  *recorder
	streams *corehttp.Streams
	put     func(key string, data []byte)
	sets    []controlplane.FlagSet
	flags   map[string][]byte
	url     string
	gen     uint64
}

// publish writes the objects, then the manifest of the next generation.
func (c *v2) publish(t testing.TB) {
	t.Helper()

	c.gen++

	doc, err := controlplane.MarshalFlagSet(&controlplane.FlagSetDocument{FlagSets: c.sets})
	require.NoError(t, err)

	manifest := &controlplane.Manifest{
		APIVersion: controlplane.APIVersion, Kind: controlplane.DocumentKindManifest, Channel: channelName,
		Generation: c.gen,
	}

	add := func(name, kind string, data []byte) {
		sum := controlplane.ChecksumOf(data)
		key, keyErr := controlplane.ObjectKey(sum, controlplane.FormatYAML)
		require.NoError(t, keyErr)
		c.put(key, data)

		manifest.Objects = append(manifest.Objects, controlplane.ManifestObject{
			Name: name, Kind: kind, Connection: connName, Key: key, Format: controlplane.FormatYAML,
			Checksum: sum, Size: int64(len(data)),
		})
	}

	add(setsObject, controlplane.KindFlagSets, doc)

	for name, data := range c.flags {
		add(name, controlplane.KindFlags, data)
	}

	data, err := controlplane.MarshalManifest(manifest)
	require.NoError(t, err)
	c.put(manifestKey, data)
}

// poll ticks the reader once, waits for its round to end and for the engine to apply the generation.
func (c *v2) poll(t testing.TB) {
	t.Helper()

	select {
	case c.sched.release <- struct{}{}:
	case <-time.After(waitBound):
		require.FailNow(t, "the reader is not waiting for a tick")
	}

	c.settle(t)
	c.awaitGeneration(t, c.gen)
}

// settle waits until the reader ended a round and waits for its next tick.
func (c *v2) settle(t testing.TB) {
	t.Helper()

	select {
	case <-c.sched.entered:
	case <-time.After(waitBound):
		require.FailNow(t, "the reader did not end its round")
	}
}

// awaitGeneration waits for the engine to apply the generation.
func (c *v2) awaitGeneration(t testing.TB, gen uint64) {
	t.Helper()

	timeout := time.After(waitBound)

	for {
		select {
		case res := <-c.rounds:
			if res.Outcome == flagruntime.OutcomeApplied && res.Generation == gen {
				return
			}
		case <-timeout:
			require.FailNow(t, "the engine did not apply the generation", "generation %d", gen)
		}
	}
}

// newV2 serves flagsYAML from a core on a directory, behind the HTTP server of the core.
func newV2(t testing.TB) *v2 {
	t.Helper()

	dir := filepath.Join(t.TempDir(), "control")
	require.NoError(t, os.MkdirAll(dir, 0o750))

	c := &v2{
		sched:   &tickScheduler{entered: make(chan struct{}, 64), release: make(chan struct{})},
		rounds:  make(chan flagruntime.RoundResult, 256),
		events:  &recorder{got: make(chan *exporter.FeatureEvent, 256)},
		streams: corehttp.NewStreams(),
		flags:   map[string][]byte{flagsObject: []byte(flagsYAML)},
		sets:    []controlplane.FlagSet{{Name: setName, Sources: []string{flagsObject}, APIKeys: []string{tenantKey}}},
		put: func(key string, data []byte) {
			path := filepath.Join(dir, key)
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
			require.NoError(t, os.WriteFile(path, data, 0o600))
		},
	}
	t.Cleanup(c.streams.Close)
	c.publish(t)

	doc := "version: 2\nconnections:\n  " + connName + ": {type: file, path: " + dir + "}\n" +
		"controlPlane:\n  connection: " + connName + "\n  manifest: {key: " + manifestKey + ", channel: " +
		channelName + ", jitter: 0}\nexporters:\n  tracked: {kind: log}\nauth:\n  adminKeys: [" + adminKey + "]\n"

	cfg, err := bootstrap.Parse([]byte(doc), nil, bootstrap.LoadOptions{})
	require.NoError(t, err)

	deps := &relaycore.Deps{
		Scheduler: c.sched,
		Broadcast: c.streams.Broadcast,
		NewManager: func(string, exporter.Config, *slog.Logger) events.FeatureManager {
			return c.events
		},
		Hooks: flagruntime.Hooks{AfterRound: func(res flagruntime.RoundResult) { c.rounds <- res }},
		TuneReader: func(rc *manifestreader.Config) {
			rc.FetchConcurrency = 1
			rc.DisableFetchSpread = true
		},
	}

	ctx, cancel := context.WithCancel(t.Context())

	core, err := relaycore.New(ctx, cfg, deps)
	require.NoError(t, err)

	c.core = core
	done := make(chan error, 1)

	go func() { done <- core.Run(ctx) }()

	t.Cleanup(func() {
		cancel()

		stop, stopCancel := context.WithTimeout(context.Background(), waitBound)
		defer stopCancel()

		require.NoError(t, core.Shutdown(stop))
	})

	c.settle(t)
	c.awaitGeneration(t, 1)

	srv, err := corehttp.New(core, &corehttp.Config{Settings: &api.Settings{}, Streams: c.streams})
	require.NoError(t, err)

	c.url = serve(t, &srv)

	return c
}

// do sends a request with the headers and returns the status, the headers and the body.
func do(t testing.TB, method, url, body string, headers http.Header) (status int, header http.Header, out []byte) {
	t.Helper()

	var reader io.Reader = http.NoBody
	if body != "" {
		reader = strings.NewReader(body)
	}

	req, err := http.NewRequestWithContext(t.Context(), method, url, reader)
	require.NoError(t, err)

	for name, values := range headers {
		req.Header[name] = values
	}

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	defer func() { require.NoError(t, resp.Body.Close()) }()

	out, err = io.ReadAll(resp.Body)
	require.NoError(t, err)

	return resp.StatusCode, resp.Header, out
}

package corehttp_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"maps"
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
  metadata: {team: core, defaultValue: false, description: a boolean}
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
func serve(tb testing.TB, srv *api.Server) string {
	tb.Helper()

	ts := httptest.NewServer(srv.Handler())
	tb.Cleanup(ts.Close)

	return ts.URL
}

// newV1Manager is the v1 flag set manager of flagsYAML, with a file retriever.
func newV1Manager(tb testing.TB) (*config.Config, *service.FlagsetManagerImpl) {
	tb.Helper()

	path := filepath.Join(tb.TempDir(), "flags.yaml")
	require.NoError(tb, os.WriteFile(path, []byte(flagsYAML), 0o600))

	conf := &config.Config{
		FlagSets: []config.FlagSet{{
			Name: setName, APIKeys: []string{tenantKey},
			Retriever: &retrieverconf.RetrieverConf{Kind: retrieverconf.FileRetriever, Path: path},
		}},
		AuthorizedKeys: config.APIKeys{Admin: []string{adminKey}},
	}
	conf.ForceReloadAPIKeys()

	manager, err := service.NewFlagsetManager(conf, slog.New(slog.DiscardHandler), nil, nil, nil)
	require.NoError(tb, err)
	tb.Cleanup(manager.Close)

	return conf, manager
}

// newV1 serves flagsYAML from the v1 relay behind the same handlers.
func newV1(tb testing.TB) string {
	tb.Helper()

	conf, manager := newV1Manager(tb)
	logger := slog.New(slog.DiscardHandler)

	metrics, err := metric.NewMetrics()
	require.NoError(tb, err)

	srv := api.New(&api.Settings{}, service.NewAPIKeys(conf), &service.Services{
		MonitoringService: service.NewMonitoring(manager),
		FlagsetManager:    manager,
		Metrics:           metrics,
	}, logger)

	return serve(tb, &srv)
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
	flags   map[string][]byte
	url     string
	sets    []controlplane.FlagSet
	gen     uint64
}

// publish writes the objects, then the manifest of the next generation.
func (rig *v2) publish(tb testing.TB) {
	tb.Helper()

	rig.gen++

	doc, err := controlplane.MarshalFlagSet(&controlplane.FlagSetDocument{FlagSets: rig.sets})
	require.NoError(tb, err)

	manifest := &controlplane.Manifest{
		APIVersion: controlplane.APIVersion, Kind: controlplane.DocumentKindManifest, Channel: channelName,
		Generation: rig.gen,
	}

	add := func(name, kind string, data []byte) {
		sum := controlplane.ChecksumOf(data)
		key, keyErr := controlplane.ObjectKey(sum, controlplane.FormatYAML)
		require.NoError(tb, keyErr)
		rig.put(key, data)

		manifest.Objects = append(manifest.Objects, controlplane.ManifestObject{
			Name: name, Kind: kind, Connection: connName, Key: key, Format: controlplane.FormatYAML,
			Checksum: sum, Size: int64(len(data)),
		})
	}

	add(setsObject, controlplane.KindFlagSets, doc)

	for name, data := range rig.flags {
		add(name, controlplane.KindFlags, data)
	}

	data, err := controlplane.MarshalManifest(manifest)
	require.NoError(tb, err)
	rig.put(manifestKey, data)
}

// poll ticks the reader once, waits for its round to end and for the engine to apply the generation.
func (rig *v2) poll(tb testing.TB) {
	tb.Helper()

	select {
	case rig.sched.release <- struct{}{}:
	case <-time.After(waitBound):
		require.FailNow(tb, "the reader is not waiting for a tick")
	}

	rig.settle(tb)
	rig.awaitGeneration(tb, rig.gen)
}

// settle waits until the reader ended a round and waits for its next tick.
func (rig *v2) settle(tb testing.TB) {
	tb.Helper()

	select {
	case <-rig.sched.entered:
	case <-time.After(waitBound):
		require.FailNow(tb, "the reader did not end its round")
	}
}

// awaitGeneration waits for the engine to apply the generation.
func (rig *v2) awaitGeneration(tb testing.TB, gen uint64) {
	tb.Helper()

	timeout := time.After(waitBound)

	for {
		select {
		case res := <-rig.rounds:
			if res.Outcome == flagruntime.OutcomeApplied && res.Generation == gen {
				return
			}
		case <-timeout:
			require.FailNow(tb, "the engine did not apply the generation", "generation %d", gen)
		}
	}
}

// newV2 serves flagsYAML from a core on a directory, behind the HTTP server of the core.
func newV2(tb testing.TB) *v2 {
	tb.Helper()

	dir := filepath.Join(tb.TempDir(), "control")
	require.NoError(tb, os.MkdirAll(dir, 0o750))

	rig := &v2{
		sched:   &tickScheduler{entered: make(chan struct{}, 64), release: make(chan struct{})},
		rounds:  make(chan flagruntime.RoundResult, 256),
		events:  &recorder{got: make(chan *exporter.FeatureEvent, 256)},
		streams: corehttp.NewStreams(),
		flags:   map[string][]byte{flagsObject: []byte(flagsYAML)},
		sets:    []controlplane.FlagSet{{Name: setName, Sources: []string{flagsObject}, APIKeys: []string{tenantKey}}},
		put: func(key string, data []byte) {
			path := filepath.Join(dir, key)
			require.NoError(tb, os.MkdirAll(filepath.Dir(path), 0o750))
			require.NoError(tb, os.WriteFile(path, data, 0o600))
		},
	}

	tb.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), waitBound)
		defer cancel()

		require.NoError(tb, rig.streams.Close(ctx))
	})
	rig.publish(tb)

	doc := "version: 2\nconnections:\n  " + connName + ": {type: file, path: " + dir + "}\n" +
		"controlPlane:\n  connection: " + connName + "\n  manifest: {key: " + manifestKey + ", channel: " +
		channelName + ", jitter: 0}\nexporters:\n  tracked: {kind: log}\nauth:\n  adminKeys: [" + adminKey + "]\n"

	cfg, err := bootstrap.Parse([]byte(doc), nil, bootstrap.LoadOptions{})
	require.NoError(tb, err)

	deps := &relaycore.Deps{
		Scheduler: rig.sched,
		Broadcast: rig.streams.Broadcast,
		NewManager: func(string, exporter.Config, *slog.Logger) events.FeatureManager {
			return rig.events
		},
		Hooks: flagruntime.Hooks{AfterRound: func(res flagruntime.RoundResult) { rig.rounds <- res }},
		TuneReader: func(rc *manifestreader.Config) {
			rc.FetchConcurrency = 1
			rc.DisableFetchSpread = true
		},
	}

	ctx, cancel := context.WithCancel(tb.Context())

	core, err := relaycore.New(ctx, cfg, deps)
	require.NoError(tb, err)

	rig.core = core
	done := make(chan error, 1)

	go func() { done <- core.Run(ctx) }()

	tb.Cleanup(func() {
		cancel()

		stop, stopCancel := context.WithTimeout(context.Background(), waitBound)
		defer stopCancel()

		require.NoError(tb, core.Shutdown(stop))
	})

	rig.settle(tb)
	rig.awaitGeneration(tb, 1)

	srv, err := corehttp.New(core, &corehttp.Config{Settings: &api.Settings{}, Streams: rig.streams})
	require.NoError(tb, err)

	rig.url = serve(tb, &srv)

	return rig
}

// do sends a request with the headers and returns the status, the headers and the body.
func do(tb testing.TB, method, url, body string, headers http.Header) (int, http.Header, []byte) {
	tb.Helper()

	var reader io.Reader = http.NoBody
	if body != "" {
		reader = strings.NewReader(body)
	}

	req, err := http.NewRequestWithContext(tb.Context(), method, url, reader)
	require.NoError(tb, err)

	maps.Copy(req.Header, headers)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(tb, err)

	defer func() { require.NoError(tb, resp.Body.Close()) }()

	out, err := io.ReadAll(resp.Body)
	require.NoError(tb, err)

	return resp.StatusCode, resp.Header, out
}

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
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/corehttp"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/events"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/relaycore"
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
	// segments are the segments objects that publish writes, by name.
	segments map[string][]byte
	url      string
	dir      string
	metrics  *metric.Metrics
	ready    *corehttp.Readiness
	// base is the bootstrap file without its admin keys.
	base string
	sets []controlplane.FlagSet
	gen  uint64
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

	for name, data := range rig.segments {
		add(name, controlplane.KindSegments, data)
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

	return newV2With(tb, &v2Options{})
}

// v2Options shape a v2 rig: a state directory, and a cold start (nothing is published, so the first poll finds no
// manifest). dir is the store directory; empty makes a new one.
type v2Options struct {
	// beforeSwap vetoes a candidate runtime (a rejected round); nil lets every candidate through.
	beforeSwap func(*flagruntime.Runtime) error
	// logger receives the logs of the core; nil discards them.
	logger   *slog.Logger
	stateDir string
	dir      string
	// connections are more connections of the bootstrap file, as indented lines of its connections block.
	connections string
	// extra is more top-level sections of the bootstrap file.
	extra       string
	cold        bool
	noAdminKeys bool
}

// newV2With is [newV2] with options.
func newV2With(tb testing.TB, opts *v2Options) *v2 {
	tb.Helper()

	return buildV2(tb, &opts, nil)
}

// buildV2 is [newV2With] with the manager of the exporters of the core; nil gives the recorder of the rig.
func buildV2(tb testing.TB, opts *v2Options, newManager events.NewManagerFunc) *v2 {
	tb.Helper()

	dir := opts.dir
	if dir == "" {
		dir = filepath.Join(tb.TempDir(), "control")
	}

	require.NoError(tb, os.MkdirAll(dir, 0o750))

	rig := &v2{
		sched:   &tickScheduler{entered: make(chan struct{}, 64), release: make(chan struct{})},
		rounds:  make(chan flagruntime.RoundResult, 256),
		events:  &recorder{got: make(chan *exporter.FeatureEvent, 256)},
		streams: corehttp.NewStreams(context.Background()),
		dir:     dir,
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

	if !opts.cold {
		rig.publish(tb)
	}

	stateLine := ""
	if opts.stateDir != "" {
		stateLine = "  stateDir: " + opts.stateDir + "\n"
	}

	rig.base = "version: 2\nconnections:\n  " + connName + ": {type: file, path: " + dir + "}\n" + opts.connections +
		"controlPlane:\n  connection: " + connName + "\n  manifest: {key: " + manifestKey + ", channel: " +
		channelName + ", jitter: 0}\n" + stateLine + "exporters:\n  tracked: {kind: log}\n"

	adminLine := "auth:\n  adminKeys: [" + adminKey + "]\n"
	if opts.noAdminKeys {
		adminLine = ""
	}

	doc := "version: 2\nconnections:\n  " + connName + ": {type: file, path: " + dir + "}\n" + opts.connections +
		"controlPlane:\n  connection: " + connName + "\n  manifest: {key: " + manifestKey + ", channel: " +
		channelName + ", jitter: 0}\n" + stateLine + "exporters:\n  tracked: {kind: log}\n" +
		adminLine + opts.extra

	cfg, err := bootstrap.Parse([]byte(doc), nil, bootstrap.LoadOptions{})
	require.NoError(tb, err)

	if newManager == nil {
		newManager = func(string, exporter.Config, *slog.Logger) events.FeatureManager { return rig.events }
	}

	deps := &relaycore.Deps{
		Logger:     opts.logger,
		Scheduler:  rig.sched,
		Broadcast:  rig.streams.Broadcast,
		NewManager: newManager,
		Hooks: flagruntime.Hooks{
			AfterRound: func(res flagruntime.RoundResult) { rig.rounds <- res },
			BeforeSwap: opts.beforeSwap,
		},
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

	switch {
	case !opts.cold:
		rig.awaitGeneration(tb, 1)
	case opts.stateDir == "":
		// the bootstrap round (applied, or ignored when the file equals the empty runtime), before the first manifest
		select {
		case <-rig.rounds:
		case <-time.After(waitBound):
			require.FailNow(tb, "the engine did not run the bootstrap round")
		}
	}

	metrics, err := metric.NewMetrics()
	require.NoError(tb, err)

	rig.metrics = metrics
	rig.ready = corehttp.NewReadiness(core)

	srv, err := corehttp.New(core, &corehttp.Config{
		Settings: &api.Settings{}, Streams: rig.streams, Metrics: metrics, Readiness: rig.ready,
	})
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

// awaitRejected waits for the engine to reject the generation.
func (rig *v2) awaitRejected(tb testing.TB, gen uint64) {
	tb.Helper()

	timeout := time.After(waitBound)

	for {
		select {
		case res := <-rig.rounds:
			if res.Outcome == flagruntime.OutcomeRejected && res.Generation == gen {
				return
			}
		case <-timeout:
			require.FailNow(tb, "the engine did not reject the generation", "generation %d", gen)
		}
	}
}

// setAdmin reloads the bootstrap file with the admin keys and waits for the engine to apply it.
func (rig *v2) setAdmin(tb testing.TB, keys string) {
	tb.Helper()

	cfg, err := bootstrap.Parse([]byte(rig.base+"auth:\n  adminKeys: ["+keys+"]\n"), nil, bootstrap.LoadOptions{})
	require.NoError(tb, err)
	require.NoError(tb, rig.core.Reload(cfg))

	timeout := time.After(waitBound)

	for {
		select {
		case res := <-rig.rounds:
			if res.Outcome == flagruntime.OutcomeApplied && !res.ContentApplied {
				return
			}
		case <-timeout:
			require.FailNow(tb, "the engine did not apply the bootstrap file")
		}
	}
}

package relaycore_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/events"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/relaycore"
	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/internal/manifestreader"
)

const (
	// waitBound bounds every wait of the tests; it is a failure bound, never a pause.
	waitBound   = 30 * time.Second
	connName    = "control"
	channelName = "stable"
	manifestKey = "manifests/stable.yaml"
	flagName    = "f"
	objA        = "flags-a"
	objB        = "flags-b"
	hookEnv     = "HOOK_URL"
	setsName    = "sets"
)

// tickScheduler is the scheduler of the reader that the test drives: a round per tick, no timer.
type tickScheduler struct {
	entered chan struct{}
	release chan struct{}
}

func newTickScheduler() *tickScheduler {
	return &tickScheduler{entered: make(chan struct{}, 64), release: make(chan struct{})}
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

// publication is what a publisher writes: flags objects by name, the document of flag sets, and the manifest.
type publication struct {
	t     *testing.T
	put   func(key string, data []byte)
	flags map[string][]byte
	sets  []controlplane.FlagSet
	gen   uint64
}

func (p *publication) entry(name, kind string, data []byte) (controlplane.ManifestObject, []byte) {
	p.t.Helper()

	sum := controlplane.ChecksumOf(data)
	key, err := controlplane.ObjectKey(sum, controlplane.FormatYAML)
	require.NoError(p.t, err)

	return controlplane.ManifestObject{
		Name: name, Kind: kind, Connection: connName, Key: key, Format: controlplane.FormatYAML,
		Checksum: sum, Size: int64(len(data)),
	}, data
}

// publish writes every object, then the manifest of the next generation.
func (p *publication) publish() {
	p.t.Helper()

	p.gen++

	doc, err := controlplane.MarshalFlagSet(&controlplane.FlagSetDocument{FlagSets: p.sets})
	require.NoError(p.t, err)

	manifest := &controlplane.Manifest{
		APIVersion: controlplane.APIVersion, Kind: controlplane.DocumentKindManifest, Channel: channelName,
		Generation: p.gen,
	}

	add := func(name, kind string, data []byte) {
		entry, body := p.entry(name, kind, data)
		p.put(entry.Key, body)

		manifest.Objects = append(manifest.Objects, entry)
	}

	add(setsName, controlplane.KindFlagSets, doc)

	for name, data := range p.flags {
		add(name, controlplane.KindFlags, data)
	}

	data, err := controlplane.MarshalManifest(manifest)
	require.NoError(p.t, err)

	p.put(manifestKey, data)
}

// flagsYAML is a flags object with the flag f, valued v, that tracks its evaluations.
func flagsYAML(value int) []byte {
	return fmt.Appendf(nil, "%s:\n  variations:\n    v: %d\n  defaultRule:\n    variation: v\n  trackEvents: true\n",
		flagName, value)
}

func filePut(t *testing.T, dir string) func(string, []byte) {
	t.Helper()

	return func(key string, data []byte) {
		path := filepath.Join(dir, key)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, data, 0o600))
	}
}

// fileDoc is a bootstrap file whose control plane is a directory.
func fileDoc(dir, stateDir string) string {
	doc := "version: 2\nconnections:\n  " + connName + ": {type: file, path: " + dir + "}\n" + controlDoc(
		connName,
		stateDir,
	)

	return doc
}

func controlDoc(conn, stateDir string) string {
	doc := "controlPlane:\n  connection: " + conn + "\n  manifest: {key: " + manifestKey + ", channel: " +
		channelName + ", jitter: 0}\n"
	if stateDir != "" {
		doc += "  stateDir: " + stateDir + "\n"
	}

	return doc + "exporters:\n  tracked: {kind: log}\nnotifiers:\n  hook: {kind: webhook, endpointUrl: {fromEnv: " +
		hookEnv + "}}\n"
}

func secretOptions(env map[string]string) bootstrap.LoadOptions {
	return bootstrap.LoadOptions{LookupEnv: func(name string) (string, bool) {
		v, ok := env[name]

		return v, ok
	}}
}

func parse(t *testing.T, doc string, opts bootstrap.LoadOptions) *bootstrap.Config {
	t.Helper()

	cfg, err := bootstrap.Parse([]byte(doc), nil, opts)
	require.NoError(t, err)

	return cfg
}

// rig is a core that runs on a bootstrap file, with the reader driven by the test.
type rig struct {
	core    *relaycore.Core
	sched   *tickScheduler
	rounds  chan flagruntime.RoundResult
	exports *recorder
	runDone chan error
	cancel  context.CancelFunc
	// wantShutdownErr is the error the cleanup expects from Shutdown; nil means none.
	wantShutdownErr error
}

// newDeps are the dependencies of a rig; the caller can adjust them before New.
func newDeps(t *testing.T, opts bootstrap.LoadOptions) (*relaycore.Deps, *rig) {
	t.Helper()

	harness := &rig{
		sched:   newTickScheduler(),
		rounds:  make(chan flagruntime.RoundResult, 256),
		exports: &recorder{got: make(chan *exporter.FeatureEvent, 256)},
		runDone: make(chan error, 1),
	}

	return &relaycore.Deps{
		Scheduler: harness.sched,
		Secrets:   opts,
		NewManager: func(string, exporter.Config, *slog.Logger) events.FeatureManager {
			return harness.exports
		},
		Hooks: flagruntime.Hooks{AfterRound: func(res flagruntime.RoundResult) { harness.rounds <- res }},
		TuneReader: func(cfg *manifestreader.Config) {
			cfg.FetchConcurrency = 1
			cfg.DisableFetchSpread = true
		},
	}, harness
}

// start builds the core and runs it; the cleanup shuts it down.
func (r *rig) start(t *testing.T, cfg *bootstrap.Config, deps *relaycore.Deps) {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	r.cancel = cancel

	core, err := relaycore.New(ctx, cfg, deps)
	require.NoError(t, err)

	r.core = core

	go func() { r.runDone <- core.Run(ctx) }()

	t.Cleanup(func() {
		cancel()

		stop, stopCancel := context.WithTimeout(context.Background(), waitBound)
		defer stopCancel()

		shutErr := core.Shutdown(stop)
		if r.wantShutdownErr != nil {
			require.ErrorIs(t, shutErr, r.wantShutdownErr)

			return
		}

		require.NoError(t, shutErr)
	})
}

// settle waits until the reader ended a round and waits for its next tick.
func (r *rig) settle(t *testing.T) {
	t.Helper()

	select {
	case <-r.sched.entered:
	case <-time.After(waitBound):
		require.FailNow(t, "the reader did not end its round")
	}
}

// poll ticks the reader once and waits for its round to end.
func (r *rig) poll(t *testing.T) {
	t.Helper()

	select {
	case r.sched.release <- struct{}{}:
	case <-time.After(waitBound):
		require.FailNow(t, "the reader is not waiting for a tick")
	}

	r.settle(t)
}

// awaitGeneration waits for the engine to apply the generation.
func (r *rig) awaitGeneration(t *testing.T, gen uint64) {
	t.Helper()

	timeout := time.After(waitBound)

	for {
		select {
		case res := <-r.rounds:
			if res.Outcome == flagruntime.OutcomeApplied && res.Generation == gen {
				return
			}
		case <-timeout:
			require.FailNow(t, "the engine did not apply the generation", "generation %d", gen)
		}
	}
}

// value evaluates the flag of the flag set that a key routes to.
func (r *rig) value(key string) (any, flagruntime.Route) {
	set, route := r.core.Holder().Load().Route(key)
	if set == nil {
		return nil, route
	}

	res, _ := r.core.Evaluator().RawVariation(set, flagName, ffcontext.NewEvaluationContext("user"), nil)

	return res.Value, route
}

// goroutinesLabelled counts the goroutines that carry the label.
func goroutinesLabelled(key, value string) []string {
	var profile bytes.Buffer

	_ = pprof.Lookup("goroutine").WriteTo(&profile, 1)

	var out []string

	for group := range strings.SplitSeq(profile.String(), "\n\n") {
		for line := range strings.SplitSeq(group, "\n") {
			raw, found := strings.CutPrefix(line, "# labels: ")
			if !found {
				continue
			}

			var labels map[string]string
			if json.Unmarshal([]byte(raw), &labels) == nil && labels[key] == value {
				out = append(out, group)
			}
		}
	}

	return out
}

// settledGoroutines gives the goroutines of the label a bounded number of yields to end.
func settledGoroutines(key, value string) []string {
	var left []string

	for range 500 {
		left = goroutinesLabelled(key, value)
		if len(left) == 0 {
			return nil
		}

		for range 200 {
			runtime.Gosched()
		}
	}

	return left
}

// awaitResult waits for the result of a Run, bounded.
func awaitResult(t *testing.T, done <-chan error) error {
	t.Helper()

	select {
	case err := <-done:
		return err
	case <-time.After(waitBound):
		require.FailNow(t, "Run did not return")
	}

	return nil
}

// awaitRun waits for Run to return nil.
func (r *rig) awaitRun(t *testing.T) {
	t.Helper()

	require.NoError(t, awaitResult(t, r.runDone))
}

// syncBuffer is a log destination that several goroutines write.
type syncBuffer struct {
	buf bytes.Buffer
	mu  sync.Mutex
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/relaycore"
	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/internal/manifestreader"
)

const (
	// waitBound bounds every wait of the tests: it is a failure bound, never a pause.
	waitBound   = 8 * time.Second
	connName    = "control"
	manifestKey = "manifests/stable.yaml"
	channelName = "stable"
	setsName    = "sets"
	flagsObject = "flags-a"
	tenantKey   = "11111111-1111-4111-8111-111111111111"
	adminKey    = "22222222-2222-4222-8222-222222222222"
	flagKey     = "f"
	evalPath    = "/ofrep/v1/evaluate/flags/" + flagKey
	readyPath   = "/readyz"
	reasonDown  = "shutting-down"
)

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

// syncBuffer is a log sink that the test can read while the relay writes to it.
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

// store is a directory that a publisher fills: the objects by content, and the manifest last.
type store struct {
	t   *testing.T
	dir string
	gen uint64
}

func (s *store) put(key string, data []byte) {
	s.t.Helper()

	path := filepath.Join(s.dir, key)
	require.NoError(s.t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(s.t, os.WriteFile(path, data, 0o600))
}

// publish writes the flag set of tenantKey over one flags object that holds the flag f valued value, then the
// manifest of the next generation.
func (s *store) publish(value int) {
	s.t.Helper()

	s.gen++

	manifest := &controlplane.Manifest{
		APIVersion: controlplane.APIVersion, Kind: controlplane.DocumentKindManifest, Channel: channelName,
		Generation: s.gen,
	}

	add := func(name, kind string, data []byte) {
		sum := controlplane.ChecksumOf(data)
		key, err := controlplane.ObjectKey(sum, controlplane.FormatYAML)
		require.NoError(s.t, err)
		s.put(key, data)

		manifest.Objects = append(manifest.Objects, controlplane.ManifestObject{
			Name: name, Kind: kind, Connection: connName, Key: key, Format: controlplane.FormatYAML,
			Checksum: sum, Size: int64(len(data)),
		})
	}

	sets, err := controlplane.MarshalFlagSet(&controlplane.FlagSetDocument{FlagSets: []controlplane.FlagSet{
		{Name: "tenant", Sources: []string{flagsObject}, APIKeys: []string{tenantKey}},
	}})
	require.NoError(s.t, err)

	add(setsName, controlplane.KindFlagSets, sets)
	add(flagsObject, controlplane.KindFlags, fmt.Appendf(nil,
		"%s:\n  variations:\n    v: %d\n  defaultRule:\n    variation: v\n", flagKey, value))

	data, err := controlplane.MarshalManifest(manifest)
	require.NoError(s.t, err)
	s.put(manifestKey, data)
}

// bootstrapDoc is a bootstrap file on the file store of dir.
func bootstrapDoc(dir, extra string) string {
	return "version: 2\nconnections:\n  " + connName + ": {type: file, path: " + dir + "}\n" +
		"controlPlane:\n  connection: " + connName + "\n  manifest: {key: " + manifestKey + ", channel: " +
		channelName + ", jitter: 0}\n" + extra
}

// relayRig is a relay started by run on a file store, with its listener open from the start.
type relayRig struct {
	stderr      *syncBuffer
	stdout      *bytes.Buffer
	rounds      chan flagruntime.RoundResult
	reload      chan struct{}
	done        chan int
	finished    chan struct{}
	sched       *tickScheduler
	draining    chan struct{}
	store       *store
	cancel      context.CancelFunc
	url         string
	cfgPath     string
	drainBody   string
	drainStatus int
	snapshot    uint64
}

type rigOptions struct {
	// extra is appended to the bootstrap file.
	extra string
	// watchDrain records the readiness at the start of the shutdown.
	watchDrain bool
}

func startRelay(t *testing.T, ropts rigOptions) *relayRig {
	t.Helper()

	dir := t.TempDir()
	rig := &relayRig{
		store:    &store{t: t, dir: dir},
		sched:    &tickScheduler{entered: make(chan struct{}, 64), release: make(chan struct{})},
		rounds:   make(chan flagruntime.RoundResult, 256),
		reload:   make(chan struct{}, 1),
		done:     make(chan int, 1),
		finished: make(chan struct{}),
		stderr:   &syncBuffer{},
		stdout:   &bytes.Buffer{},
		cfgPath:  filepath.Join(t.TempDir(), "bootstrap.yaml"),
		draining: make(chan struct{}),
	}
	rig.store.publish(1)
	rig.writeConfig(ropts.extra)

	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	rig.url = "http://" + listener.Addr().String()

	ctx, cancel := context.WithCancel(t.Context())
	rig.cancel = cancel

	opts := &runOptions{
		Args:   []string{configFlag, rig.cfgPath},
		Stdout: rig.stdout, Stderr: rig.stderr,
		Load:   bootstrap.LoadOptions{ReadFile: os.ReadFile},
		Reload: rig.reload,
		Deps: relaycore.Deps{
			Scheduler: rig.sched,
			TuneReader: func(cfg *manifestreader.Config) {
				cfg.DisableFetchSpread = true
			},
			Hooks: flagruntime.Hooks{AfterRound: func(res flagruntime.RoundResult) { rig.rounds <- res }},
		},
		ServerOptions: []api.Option{api.WithListener(listener)},
	}
	if ropts.watchDrain {
		opts.OnDraining = rig.onDraining
	}

	go func() {
		defer close(rig.finished)

		rig.done <- run(ctx, opts)
	}()

	t.Cleanup(func() {
		cancel()
		<-rig.finished
	})

	return rig
}

// onDraining records what /readyz answers while the listener is still open.
func (r *relayRig) onDraining() {
	status, body := get(r.store.t, r.url+readyPath, nil)
	r.drainStatus, r.drainBody = status, body
	close(r.draining)
}

func (r *relayRig) writeConfig(extra string) {
	r.store.t.Helper()
	require.NoError(r.store.t, os.WriteFile(r.cfgPath, []byte(bootstrapDoc(r.store.dir, extra)), 0o600))
}

// awaitRound waits for the first applied round that satisfies ok.
func (r *relayRig) awaitRound(ok func(flagruntime.RoundResult) bool) {
	r.store.t.Helper()

	timeout := time.After(waitBound)

	for {
		select {
		case res := <-r.rounds:
			if res.Outcome == flagruntime.OutcomeApplied && ok(res) {
				r.snapshot = res.Snapshot

				return
			}
		case <-timeout:
			require.FailNow(r.store.t, "the engine did not apply the expected round", r.stderr.String())
		}
	}
}

// awaitGeneration waits for the engine to apply the generation.
func (r *relayRig) awaitGeneration(gen uint64) {
	r.store.t.Helper()
	r.awaitRound(func(res flagruntime.RoundResult) bool { return res.Generation == gen })
}

// poll lets the reader poll once and waits for the generation to be live.
func (r *relayRig) poll(gen uint64) {
	r.store.t.Helper()

	select {
	case <-r.sched.entered:
	case <-time.After(waitBound):
		require.FailNow(r.store.t, "the reader did not end its round")
	}

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

func get(t *testing.T, url string, headers http.Header) (int, string) {
	t.Helper()

	return doRequest(t, http.MethodGet, url, "", headers)
}

func doRequest(t *testing.T, method, url, body string, headers http.Header) (int, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), method, url, strings.NewReader(body))
	require.NoError(t, err)

	maps.Copy(req.Header, headers)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	defer func() { require.NoError(t, resp.Body.Close()) }()

	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	return resp.StatusCode, string(data)
}

// evaluate evaluates the flag f with the key and returns the status and the value.
func (r *relayRig) evaluate(key string) (int, any) {
	r.store.t.Helper()

	status, body := doRequest(r.store.t, http.MethodPost, r.url+evalPath, `{"context":{"targetingKey":"u"}}`,
		http.Header{"X-Api-Key": {key}, "Content-Type": {"application/json"}})

	var out struct {
		Value any `json:"value"`
	}

	_ = json.Unmarshal([]byte(body), &out)

	return status, out.Value
}

func TestRunServesAFileStore(t *testing.T) {
	t.Parallel()

	rig := startRelay(
		t,
		rigOptions{extra: "auth:\n  adminKeys: [" + adminKey + "]\nevaluation:\n  environment: test\n"},
	)
	rig.awaitGeneration(1)

	status, body := get(t, rig.url+readyPath, nil)
	assert.Equal(t, http.StatusOK, status)
	assert.JSONEq(t, `{"ready":true,"state":"serving"}`, body)

	status, value := rig.evaluate(tenantKey)
	assert.Equal(t, http.StatusOK, status)
	assert.InDelta(t, 1, value, 0)

	status, _ = rig.evaluate("33333333-3333-4333-8333-333333333333")
	assert.Equal(t, http.StatusUnauthorized, status)

	// an object change is live after one poll, without a restart
	rig.store.publish(2)
	rig.poll(2)

	status, value = rig.evaluate(tenantKey)
	assert.Equal(t, http.StatusOK, status)
	assert.InDelta(t, 2, value, 0)

	assert.Contains(t, rig.stdout.String(), "Semaphore Relay Proxy")
}

func TestRunReloadsTheBootstrapFile(t *testing.T) {
	t.Parallel()

	rig := startRelay(t, rigOptions{})
	rig.awaitGeneration(1)

	// the admin key is unknown: 401. After the reload it is known: the evaluation API answers 400 to an admin key.
	status, _ := rig.evaluate(adminKey)
	assert.Equal(t, http.StatusUnauthorized, status)

	before := rig.snapshot

	rig.writeConfig("auth:\n  adminKeys: [" + adminKey + "]\n")

	rig.reload <- struct{}{}

	rig.awaitRound(func(res flagruntime.RoundResult) bool { return res.Snapshot > before })

	status, _ = rig.evaluate(adminKey)
	assert.Equal(t, http.StatusBadRequest, status)
}

func TestRunShutsDownInOrder(t *testing.T) {
	t.Parallel()

	rig := startRelay(t, rigOptions{watchDrain: true})
	rig.awaitGeneration(1)

	rig.cancel()

	select {
	case <-rig.draining:
	case <-time.After(waitBound):
		require.FailNow(t, "the relay did not start to drain")
	}

	// the readiness fails first, while the listener still answers
	assert.Equal(t, http.StatusServiceUnavailable, rig.drainStatus)
	assert.JSONEq(t, `{"ready":false,"reason":"`+reasonDown+`"}`, rig.drainBody)

	select {
	case code := <-rig.done:
		assert.Equal(t, 0, code)
	case <-time.After(waitBound):
		require.FailNow(t, "the relay did not stop")
	}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, rig.url+readyPath, http.NoBody)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	if err == nil {
		_ = resp.Body.Close()
	}

	require.Error(t, err, "the listener is closed after the shutdown")
}

func TestRunRefusesWhatItCanNotServe(t *testing.T) {
	t.Parallel()

	taken, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	t.Cleanup(func() { _ = taken.Close() })

	dir := t.TempDir()
	st := &store{t: t, dir: dir}
	st.publish(1)

	tests := []struct {
		name     string
		doc      string
		wantErr  string
		args     []string
		wantCode int
	}{
		{name: "a version 1 file", doc: "retrievers:\n  - kind: file\n    path: flags.yaml\n", wantCode: 1,
			wantErr: "version 2 required"},
		{name: "version 1 declared", doc: "version: 1\n", wantCode: 1, wantErr: "version 2 required"},
		{name: "an unknown key", doc: bootstrapDoc(dir, "studio: {}\n"), wantCode: 1, wantErr: "studio"},
		{name: "no control plane", doc: "version: 2\n", wantCode: 1, wantErr: "controlPlane"},
		{name: "a unix socket", doc: bootstrapDoc(dir, "server:\n  unixSocketPath: /tmp/semaphore.sock\n"), wantCode: 1,
			wantErr: "server.unixSocketPath"},
		{name: "two api listeners", doc: bootstrapDoc(dir, "server:\n  listeners:\n    - {name: a, address: \":1\"}\n"+
			"    - {name: b, address: \":2\"}\n"), wantCode: 1, wantErr: "more than one api listener"},
		{name: "a port in use", doc: bootstrapDoc(dir, "server:\n  listeners:\n    - {name: api, address: \""+
			taken.Addr().String()+"\"}\n"), wantCode: 1, wantErr: "Error starting relay proxy"},
		{name: "no file", args: []string{}, wantCode: 1, wantErr: "no bootstrap file"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			args := tt.args
			if args == nil {
				path := filepath.Join(t.TempDir(), "bootstrap.yaml")
				require.NoError(t, os.WriteFile(path, []byte(tt.doc), 0o600))

				args = []string{configFlag, path}
			}

			var stderr syncBuffer

			code := run(t.Context(), &runOptions{
				Args: args, Stdout: io.Discard, Stderr: &stderr, Load: bootstrap.LoadOptions{ReadFile: os.ReadFile},
				ShutdownTimeout: waitBound,
			})

			assert.Equal(t, tt.wantCode, code)
			assert.Contains(t, stderr.String(), tt.wantErr)
		})
	}
}

func TestRunPrintsTheVersion(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer

	code := run(t.Context(), &runOptions{Args: []string{versionFlag}, Stdout: &stdout, Stderr: io.Discard})

	assert.Equal(t, 0, code)
	assert.True(t, strings.HasPrefix(stdout.String(), "semaphore "), stdout.String())
}

func TestLogFormat(t *testing.T) {
	t.Parallel()

	tests := []struct{ name, in, want string }{
		{name: "text is logfmt", in: "text", want: "logfmt"},
		{name: "json stays", in: "json", want: "json"},
		{name: "empty stays", in: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, logFormat(tt.in))
		})
	}
}

var errLoad = errors.New("load failed")

func TestReloadRefusesAFileThatDoesNotLoad(t *testing.T) {
	t.Parallel()

	var logs syncBuffer

	rel := &relay{log: slog.New(slog.NewTextHandler(&logs, nil))}
	rel.reload(t.Context(), func() (*bootstrap.Config, error) { return nil, errLoad })

	assert.Contains(t, logs.String(), "the reload of the bootstrap file was refused")
	assert.Contains(t, logs.String(), errLoad.Error())
}

package relaycore_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime/pprof"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/events"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/relaycore"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/segmentstore"
	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/internal/connection"
	"github.com/blackbirdworks/semaphore/internal/manifestreader"
	"github.com/blackbirdworks/semaphore/internal/manifestreader/statedir"
)

const (
	deadURL      = "http://127.0.0.1:1"
	canaryKey    = "AKIA-CANARY-ACCESS-KEY"
	canarySecret = "CANARY-SECRET-VALUE"
)

// hookServer is the endpoint of the webhook notifier: every call is signalled on the channel.
func hookServer(t *testing.T) (string, <-chan struct{}) {
	t.Helper()

	calls := make(chan struct{}, 64)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls <- struct{}{}

		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	return srv.URL, calls
}

func twoSets(t *testing.T, put func(string, []byte)) *publication {
	t.Helper()

	return &publication{
		t: t, put: put,
		flags: map[string][]byte{objA: flagsYAML(1), objB: flagsYAML(2)},
		sets: []controlplane.FlagSet{
			{Name: "one", Sources: []string{objA}, APIKeys: []string{"key-one"}},
			{Name: "two", Sources: []string{objB}, APIKeys: []string{"key-two"}},
		},
	}
}

func TestNewErrors(t *testing.T) {
	t.Parallel()

	const awsDoc = "version: 2\nconnections:\n  s3c:\n    type: s3\n    region: us-east-1\n    bucket: b\n" +
		"    credentials:\n" +
		"      type: static\n      accessKeyId: {fromEnv: AK}\n      secretAccessKey: {fromEnv: SK}\n"

	env := map[string]string{"AK": canaryKey, "SK": canarySecret}
	hook := map[string]string{hookEnv: deadURL}
	dir := t.TempDir()

	tests := []struct {
		build    func(t *testing.T) *bootstrap.Config
		wantErr  error
		env      map[string]string
		name     string
		wantPath string
	}{
		{
			name:    "no configuration",
			build:   func(*testing.T) *bootstrap.Config { return nil },
			wantErr: relaycore.ErrNilConfig,
		},
		{
			name: "no control plane",
			build: func(t *testing.T) *bootstrap.Config {
				t.Helper()

				return parse(t, "version: 2\n", secretOptions(nil))
			},
			wantErr:  relaycore.ErrNoControlPlane,
			wantPath: "controlPlane",
		},
		{
			name: "control plane names a connection that is not there",
			build: func(t *testing.T) *bootstrap.Config {
				t.Helper()

				cfg := parse(t, fileDoc(dir, ""), secretOptions(nil))
				cfg.ControlPlane.Connection = "elsewhere"

				return cfg
			},
			wantErr:  relaycore.ErrUnknownConnection,
			wantPath: "controlPlane.connection",
		},
		{
			name: "file connection whose directory is missing",
			build: func(t *testing.T) *bootstrap.Config {
				t.Helper()

				return parse(t, fileDoc(filepath.Join(dir, "missing"), ""), secretOptions(nil))
			},
			env:      hook,
			wantErr:  manifestreader.ErrInvalidConfig,
			wantPath: "connections." + connName + ".path",
		},
		{
			name: "connection whose spec is invalid carries no credential",
			build: func(t *testing.T) *bootstrap.Config {
				t.Helper()

				cfg := parse(t, awsDoc+controlDoc("s3c", ""), secretOptions(env))
				cfg.Connections["s3c"].S3.Region = ""

				return cfg
			},
			env:      env,
			wantErr:  connection.ErrInvalidSpec,
			wantPath: "connections.s3c",
		},
		{
			name: "secret that does not resolve",
			build: func(t *testing.T) *bootstrap.Config {
				t.Helper()

				return parse(t, awsDoc+controlDoc("s3c", ""), secretOptions(env))
			},
			env:      nil,
			wantErr:  bootstrap.ErrSecretUnresolved,
			wantPath: "connections.s3c",
		},
		{
			name: "an admin key reference that does not resolve",
			build: func(t *testing.T) *bootstrap.Config {
				t.Helper()

				return parse(t, fileDoc(dir, "")+"auth:\n  adminKeys: ["+canaryKey+", {fromEnv: ADMIN_MISSING}]\n",
					secretOptions(hook))
			},
			env:      hook,
			wantErr:  flagruntime.ErrInvalidBootstrap,
			wantPath: "auth.adminKeys",
		},
		{
			name: "poll interval the reader refuses",
			build: func(t *testing.T) *bootstrap.Config {
				t.Helper()

				cfg := parse(t, fileDoc(dir, ""), secretOptions(nil))
				poll := bootstrap.Duration(time.Millisecond)
				cfg.ControlPlane.Manifest.PollInterval = &poll

				return cfg
			},
			env:      hook,
			wantErr:  manifestreader.ErrInvalidConfig,
			wantPath: "controlPlane.manifest.pollInterval",
		},
		{
			name: "jitter the reader refuses",
			build: func(t *testing.T) *bootstrap.Config {
				t.Helper()

				cfg := parse(t, fileDoc(dir, ""), secretOptions(nil))
				one := manifestreader.MaxJitterFraction
				cfg.ControlPlane.Manifest.Jitter = &one

				return cfg
			},
			env:      hook,
			wantErr:  manifestreader.ErrInvalidConfig,
			wantPath: "controlPlane.manifest.jitter",
		},
		{
			name: "exporters that do not build",
			build: func(t *testing.T) *bootstrap.Config {
				t.Helper()

				return parse(t, fileDoc(dir, ""), secretOptions(nil))
			},
			env:      nil,
			wantErr:  bootstrap.ErrSecretUnresolved,
			wantPath: "exporters, notifiers",
		},
	}

	for _, tcase := range tests {
		t.Run(tcase.name, func(t *testing.T) {
			t.Parallel()

			core, err := relaycore.New(t.Context(), tcase.build(t), &relaycore.Deps{Secrets: secretOptions(tcase.env)})
			require.Error(t, err)
			assert.Nil(t, core)

			if tcase.wantErr != nil {
				require.ErrorIs(t, err, tcase.wantErr)
			}

			if tcase.wantPath != "" {
				var built *relaycore.BuildError

				require.ErrorAs(t, err, &built)
				assert.Equal(t, tcase.wantPath, built.Path)
			}

			assert.NotContains(t, err.Error(), canaryKey)
			assert.NotContains(t, err.Error(), canarySecret)
		})
	}
}

// A build that fails after the state directory was opened releases it: the same directory opens again.
func TestFailedBuildReleasesTheStateDirectory(t *testing.T) {
	t.Parallel()

	dir, state := t.TempDir(), filepath.Join(t.TempDir(), "state")
	opts := secretOptions(map[string]string{hookEnv: deadURL})

	bad := parse(t, fileDoc(dir, state), opts)
	poll := bootstrap.Duration(time.Millisecond)
	bad.ControlPlane.Manifest.PollInterval = &poll

	_, err := relaycore.New(t.Context(), bad, &relaycore.Deps{Secrets: opts})
	require.ErrorIs(t, err, manifestreader.ErrInvalidConfig)

	core, err := relaycore.New(t.Context(), parse(t, fileDoc(dir, state), opts), &relaycore.Deps{Secrets: opts})
	require.NoError(t, err)

	assert.Empty(t, core.Status().StateDirDisabled, "the failed build did not keep the lock")
	require.NoError(t, core.Shutdown(t.Context()))
}

// The collector of the events is registered once and removed at shutdown.
func TestCollectorLifecycle(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	opts := secretOptions(map[string]string{hookEnv: deadURL})
	registry := prometheus.NewRegistry()
	deps := &relaycore.Deps{Secrets: opts, Registerer: registry}

	first, err := relaycore.New(t.Context(), parse(t, fileDoc(dir, ""), opts), deps)
	require.NoError(t, err)

	_, err = relaycore.New(t.Context(), parse(t, fileDoc(dir, ""), opts), deps)

	var built *relaycore.BuildError

	require.ErrorAs(t, err, &built)
	assert.Equal(t, "observability.metrics", built.Path)

	require.NoError(t, first.Shutdown(t.Context()))

	second, err := relaycore.New(t.Context(), parse(t, fileDoc(dir, ""), opts), deps)
	require.NoError(t, err, "the first core unregistered its collector")
	require.NoError(t, second.Shutdown(t.Context()))
}

func TestLifecycle(t *testing.T) {
	t.Parallel()

	t.Run("run then shutdown leaves no goroutine", func(t *testing.T) {
		t.Parallel()

		dir, state := t.TempDir(), filepath.Join(t.TempDir(), "state")
		opts := secretOptions(map[string]string{hookEnv: deadURL})
		cfg := parse(t, fileDoc(dir, state), opts)
		twoSets(t, filePut(t, dir)).publish()

		deps, harness := newDeps(t, opts)
		label := t.Name()

		pprof.Do(t.Context(), pprof.Labels("relaycore-test", label), func(ctx context.Context) {
			core, err := relaycore.New(ctx, cfg, deps)
			require.NoError(t, err)

			done := make(chan error, 1)

			go func() { done <- core.Run(ctx) }()

			harness.settle(t)
			assert.NotEmpty(
				t,
				goroutinesLabelled("relaycore-test", label),
				"the label reaches the goroutines of the core",
			)
			require.NoError(t, core.Shutdown(ctx))
			require.NoError(t, awaitResult(t, done))
		})

		assert.Empty(t, settledGoroutines("relaycore-test", label))
	})

	t.Run("shutdown without run", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		opts := secretOptions(map[string]string{hookEnv: deadURL})
		cfg := parse(t, fileDoc(dir, ""), opts)
		label := t.Name()

		pprof.Do(t.Context(), pprof.Labels("relaycore-test", label), func(ctx context.Context) {
			core, err := relaycore.New(ctx, cfg, &relaycore.Deps{Secrets: opts})
			require.NoError(t, err)

			require.NoError(t, core.Shutdown(ctx))
			require.ErrorIs(t, core.Run(ctx), relaycore.ErrClosed)
			require.ErrorIs(t, core.Reload(cfg), relaycore.ErrClosed)
		})

		assert.Empty(t, settledGoroutines("relaycore-test", label))
	})

	t.Run("double shutdown and double run", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		opts := secretOptions(map[string]string{hookEnv: deadURL})
		deps, harness := newDeps(t, opts)
		twoSets(t, filePut(t, dir)).publish()

		harness.start(t, parse(t, fileDoc(dir, ""), opts), deps)
		harness.settle(t)

		require.ErrorIs(t, harness.core.Run(t.Context()), relaycore.ErrAlreadyRunning)
		require.NoError(t, harness.core.Shutdown(t.Context()))
		require.NoError(t, harness.core.Shutdown(t.Context()))
		harness.awaitRun(t)
	})

	t.Run("run ends with its context and shutdown still drains", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		opts := secretOptions(map[string]string{hookEnv: deadURL})
		deps, harness := newDeps(t, opts)
		twoSets(t, filePut(t, dir)).publish()

		harness.start(t, parse(t, fileDoc(dir, ""), opts), deps)
		harness.settle(t)
		harness.cancel()

		harness.awaitRun(t)
		require.NoError(t, harness.core.Shutdown(t.Context()))
	})

	t.Run("shutdown is bounded by its context and cleans up after the loops", func(t *testing.T) {
		t.Parallel()

		dir, state := t.TempDir(), filepath.Join(t.TempDir(), "state")
		opts := secretOptions(map[string]string{hookEnv: deadURL})
		deps, harness := newDeps(t, opts)
		registry := prometheus.NewRegistry()
		deps.Registerer = registry

		twoSets(t, filePut(t, dir)).publish()

		// the engine is held in its first round, so the loops cannot end
		entered, release := make(chan struct{}, 1), make(chan struct{})

		var open sync.Once

		deps.Hooks.AfterRound = func(flagruntime.RoundResult) {
			select {
			case entered <- struct{}{}:
			default:
			}

			<-release
		}

		harness.wantShutdownErr = relaycore.ErrNotDrained
		harness.start(t, parse(t, fileDoc(dir, state), opts), deps)
		// registered after the start, so it runs before the shutdown of the cleanup: the gate opens first
		t.Cleanup(func() { open.Do(func() { close(release) }) })

		select {
		case <-entered:
		case <-time.After(waitBound):
			require.FailNow(t, "the engine did not reach its first round")
		}

		expired, cancel := context.WithCancel(t.Context())
		cancel()

		label := t.Name()

		var err error

		pprof.Do(t.Context(), pprof.Labels("relaycore-test", label), func(context.Context) {
			err = harness.core.Shutdown(expired)
		})

		require.ErrorIs(t, err, relaycore.ErrNotDrained)
		require.ErrorIs(t, err, context.Canceled)

		// the loops are held: the state directory is still locked and the collector registered
		_, err = relaycore.New(t.Context(), parse(t, fileDoc(dir, state), opts), deps)

		var built *relaycore.BuildError

		require.ErrorAs(t, err, &built)
		assert.Equal(t, "observability.metrics", built.Path)

		open.Do(func() { close(release) })
		harness.awaitRun(t)

		// the first result stands for every later call, which waits for the cleanup within its own context
		require.ErrorIs(t, harness.core.Shutdown(t.Context()), relaycore.ErrNotDrained)
		assert.Empty(t, settledGoroutines("relaycore-test", label), "the cleanup goroutine ended")

		// the cleanup ran: the registry takes a new collector, and the state directory opens again
		again, err := relaycore.New(t.Context(), parse(t, fileDoc(dir, state), opts), deps)
		require.NoError(t, err)

		assert.Empty(t, again.Status().StateDirDisabled)
		require.NoError(t, again.Shutdown(t.Context()))
	})
}

func TestReload(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	opts := secretOptions(map[string]string{hookEnv: deadURL})
	deps, harness := newDeps(t, opts)
	twoSets(t, filePut(t, dir)).publish()

	cfg := parse(t, fileDoc(dir, ""), opts)
	harness.start(t, cfg, deps)
	harness.settle(t)
	harness.awaitGeneration(t, 1)

	require.ErrorIs(t, harness.core.Reload(nil), relaycore.ErrNilConfig)
	require.ErrorIs(t, harness.core.Reload(parse(t, "version: 2\n", opts)), relaycore.ErrNoControlPlane)

	// an admin key applies live; a change of the exporters waits for a restart and is named in the status
	next := parse(t, fileDoc(dir, "")+"auth:\n  adminKeys: [admin-key-1]\n", secretOptionsInline(opts))
	next.Exporters = nil
	require.NoError(t, harness.core.Reload(next))

	require.Eventually(t, func() bool {
		_, route := harness.core.Holder().Load().Route("admin-key-1")

		return route == flagruntime.RouteAdmin
	}, waitBound, time.Millisecond)

	assert.Equal(t, []string{flagruntime.SettingExporters}, harness.core.Holder().Load().Status.PendingRestart)
	assert.Equal(t, []string{flagruntime.SettingExporters}, harness.core.Status().Source.Runtime.PendingRestart)
}

func secretOptionsInline(opts bootstrap.LoadOptions) bootstrap.LoadOptions {
	opts.AllowInlineSecrets = true

	return opts
}

// Evaluating a flag that tracks its events reaches the exporter, a change of the manifest reaches the notifier,
// and a state directory that cannot be used does not stop the relay.
func TestWiring(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	hookURL, hooks := hookServer(t)
	opts := secretOptions(map[string]string{hookEnv: hookURL})
	deps, harness := newDeps(t, opts)
	pub := twoSets(t, filePut(t, dir))
	pub.publish()

	// the state directory is a file: it is refused
	notDir := filepath.Join(t.TempDir(), "state-file")
	require.NoError(t, os.WriteFile(notDir, []byte("x"), 0o600))

	harness.start(t, parse(t, fileDoc(dir, notDir), opts), deps)
	harness.settle(t)
	harness.awaitGeneration(t, 1)

	assert.Equal(
		t,
		statedir.DisabledUnsafe,
		harness.core.Status().StateDirDisabled,
		"the status says why there is no state directory",
	)

	got, route := harness.value("key-one")
	assert.Equal(t, flagruntime.RouteFlagSet, route)
	assert.EqualValues(t, 1, got, "the relay serves without a state directory")

	select {
	case event := <-harness.exports.got:
		assert.Equal(t, flagName, event.Key)
	case <-time.After(waitBound):
		require.FailNow(t, "the evaluation of a tracked flag did not reach the exporter")
	}

	pub.flags[objA] = flagsYAML(10)
	pub.publish()
	harness.poll(t)
	harness.awaitGeneration(t, 2)

	select {
	case <-hooks:
	case <-time.After(waitBound):
		require.FailNow(t, "the change of the manifest did not reach the notifier")
	}

	status := harness.core.Status()
	assert.Equal(t, []string{"hook"}, notifierNames(&status.Events))
	assert.Equal(t, []string{"tracked"}, exporterNames(&status.Events))
	assert.Equal(t, uint64(2), status.Source.LastGeneration)
}

func notifierNames(status *events.Status) []string {
	names := make([]string, 0, len(status.Notifiers))
	for i := range status.Notifiers {
		names = append(names, status.Notifiers[i].Name)
	}

	return names
}

func exporterNames(status *events.Status) []string {
	names := make([]string, 0, len(status.Exporters))
	for i := range status.Exporters {
		names = append(names, status.Exporters[i].Name)
	}

	return names
}

// Every control plane setting that bootstrap accepts builds: the boundaries of the interval and of the jitter, and
// the shapes of the manifest key.
func TestNewBuildsEveryAcceptedBoundary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		manifest string
	}{
		{name: "interval of one second", manifest: "key: m.json, pollInterval: 1s"},
		{name: "jitter zero", manifest: "key: m.json, jitter: 0"},
		{name: "jitter just below one", manifest: "key: m.json, jitter: 0.99"},
		{name: "nested key", manifest: "key: manifests/prod/stable.yaml"},
		{name: "defaults", manifest: "key: m.json"},
	}

	for _, tcase := range tests {
		t.Run(tcase.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			doc := "version: 2\nconnections:\n  " + connName + ": {type: file, path: " + dir + "}\n" +
				"controlPlane: {connection: " + connName + ", manifest: {" + tcase.manifest + "}}\n"

			core, err := relaycore.New(t.Context(), parse(t, doc, secretOptions(nil)), &relaycore.Deps{})
			require.NoError(t, err)
			require.NoError(t, core.Shutdown(t.Context()))
		})
	}
}

// The evaluation settings of the file reach the evaluator: the enrichment of every flag set.
func TestEvaluationSettingsReachTheEvaluator(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	opts := secretOptions(map[string]string{hookEnv: "http://127.0.0.1:1"})
	deps, harness := newDeps(t, opts)
	pub := twoSets(t, filePut(t, dir))
	pub.publish()

	doc := fileDoc(dir, "") + "evaluation:\n  environment: staging\n  contextEnrichment: {region: eu}\n"
	harness.start(t, parse(t, doc, opts), deps)
	harness.settle(t)
	harness.awaitGeneration(t, 1)

	set, _ := harness.core.Holder().Load().Route("key-one")
	require.NotNil(t, set)

	assert.Equal(t, map[string]any{"region": "eu"},
		harness.core.Evaluator().EvaluationContextEnrichment(set))
}

// The segments section builds a core that opens nothing until a segment needs a store: the connection is idle, and
// the core stops cleanly.
func TestSegmentsSectionIsServedLazily(t *testing.T) {
	t.Parallel()

	opts := secretOptions(map[string]string{hookEnv: deadURL})
	doc := "version: 2\nconnections:\n  " + connName + ": {type: file, path: " + t.TempDir() + "}\n" +
		"  segstore: {type: valkey, addresses: [\"127.0.0.1:1\"]}\n" + controlDoc(connName, "") +
		"segments: {resolver: {callTimeout: 2s}}\n"

	core, err := relaycore.New(t.Context(), parse(t, doc, opts), &relaycore.Deps{Secrets: opts})
	require.NoError(t, err)

	assert.Equal(t, []segmentstore.ConnectionState{{Name: "segstore", Kind: "valkey", State: "idle"}},
		core.Status().Segments.Stores)
	require.NoError(t, core.Shutdown(t.Context()))
}

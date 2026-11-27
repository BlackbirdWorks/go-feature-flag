// Package relay starts an in-process relay proxy for the benchmarks and the soak harness.
package relay

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/corehttp"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/relaycore"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/internal/loadtest"
)

const (
	// readyTimeout is how long the relay proxy has to serve its first generation.
	readyTimeout = 15 * time.Second
	// readyPollInterval is the pause between two health requests of waitHealthy.
	readyPollInterval = 20 * time.Millisecond
	// roundsBuffer is the number of rounds of the engine that wait for Start to read them.
	roundsBuffer = 16
	// APIKey is the key of the flag set that the requests of the harness carry.
	APIKey = "loadtest-key"

	connName    = "control"
	channelName = "stable"
	manifestKey = "manifests/stable.yaml"
	flagsObject = "flags"
	setsObject  = "sets"
	setName     = "loadtest"
	storeDir    = "store"
	sinkEnv     = "LOADTEST_SINK_URL"
)

var (
	errUnexpectedListenerAddr = errors.New("unexpected listener address type")
	errNotReady               = errors.New("the relay proxy did not serve its flags")
)

// Options configures Start.
type Options struct {
	// FlagsYAML is the flag configuration (see loadtest.FlagsYAML).
	FlagsYAML string
	// ExporterMode is the behaviour of the webhook sink the relay proxy exports to.
	// An empty value disables the exporter.
	ExporterMode loadtest.ExporterMode
	// TempDir is the directory the flag file directory is created in (default: os.TempDir()).
	TempDir string
	// FlushIntervalMs is the flush interval of the exporter (default 1000).
	FlushIntervalMs int64
	// MaxEventInMemory is the flush trigger of the exporter (default 100000).
	MaxEventInMemory int64
	// H2C makes the relay proxy accept HTTP/2 over cleartext; only with Listen.
	H2C bool
	// Listen enables the real HTTP server on a free loopback port (needed for the soak).
	Listen bool
}

// Relay is a running relay proxy: a version 2 core on a file store, and its HTTP server when it listens.
type Relay struct {
	sink    *httptest.Server
	server  *api.Server
	core    *relaycore.Core
	streams *corehttp.Streams
	cancel  context.CancelFunc
	// FlagSets finds the flag set of APIKey, to build handlers in-process.
	FlagSets service.FlagSetResolver
	// Metrics are the metrics of the handlers.
	Metrics *metric.Metrics
	// BaseURL is the loopback URL of the HTTP server (only if Options.Listen).
	BaseURL string
	dir     string
	loops   sync.WaitGroup
	// SinkRequests counts the requests received by the webhook sink.
	SinkRequests atomic.Int64
	// SinkBytes counts the bytes received by the webhook sink.
	SinkBytes atomic.Int64
}

// Start starts a relay proxy.
func Start(opts Options) (_ *Relay, err error) {
	dir, err := os.MkdirTemp(opts.TempDir, "ffrelay")
	if err != nil {
		return nil, fmt.Errorf("create relay temp dir: %w", err)
	}

	r := &Relay{dir: dir}

	defer func() {
		if err != nil {
			r.Close() // releases whatever was started and removes the temp dir
		}
	}()

	err = r.publish(opts.FlagsYAML)
	if err != nil {
		return nil, err
	}

	cfg, err := r.bootstrapConfig(opts)
	if err != nil {
		return nil, err
	}

	r.Metrics, err = metric.NewMetrics()
	if err != nil {
		return nil, fmt.Errorf("init relay metrics: %w", err)
	}

	r.streams = corehttp.NewStreams(context.Background())

	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel

	rounds := make(chan flagruntime.RoundResult, roundsBuffer)

	core, err := relaycore.New(ctx, cfg, &relaycore.Deps{
		Registerer: r.Metrics.Registry,
		Broadcast:  r.streams.Broadcast,
		Secrets:    r.secrets(),
		Hooks: flagruntime.Hooks{AfterRound: func(res flagruntime.RoundResult) {
			select {
			case rounds <- res:
			default:
			}
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("init relay core: %w", err)
	}

	r.core = core
	r.FlagSets = corehttp.NewAdapter(core)

	r.loops.Go(func() { _ = core.Run(ctx) })

	err = waitApplied(rounds)
	if err != nil {
		return nil, err
	}

	err = r.checkServed()
	if err != nil {
		return nil, err
	}

	ready := corehttp.NewReadiness(core)

	if opts.Listen {
		err = r.listen(ready, opts)
		if err != nil {
			return nil, err
		}
	}

	return r, nil
}

// Dir returns the temporary directory holding the flag file (removed by Close).
func (r *Relay) Dir() string { return r.dir }

// Close stops the relay proxy and removes its temporary flag file.
func (r *Relay) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), readyTimeout)
	defer cancel()

	if r.streams != nil {
		_ = r.streams.Close(ctx)
	}

	if r.server != nil {
		r.server.Stop(ctx)
	}

	if r.core != nil {
		_ = r.core.Shutdown(ctx)
	}

	if r.cancel != nil {
		r.cancel()
	}

	r.loops.Wait()

	if r.sink != nil {
		r.sink.CloseClientConnections()
		r.sink.Close()
	}

	if r.dir != "" {
		_ = os.RemoveAll(r.dir)
	}
}

// listen serves the HTTP API on a free loopback port.
func (r *Relay) listen(ready *corehttp.Readiness, opts Options) error {
	listener, port, err := listenLoopback()
	if err != nil {
		return err
	}

	settings := api.Settings{Mode: api.ServerModeHTTP, Host: "127.0.0.1", Port: port, EnableH2C: opts.H2C}

	server, err := corehttp.New(r.core, &corehttp.Config{
		Settings: &settings, Streams: r.streams, Metrics: r.Metrics, Readiness: ready,
	}, api.WithListener(listener))
	if err != nil {
		_ = listener.Close()

		return fmt.Errorf("init relay server: %w", err)
	}

	r.server = &server
	r.loops.Go(func() { server.StartWithContext(context.Background()) })
	r.BaseURL = fmt.Sprintf("http://127.0.0.1:%d", port)

	return waitHealthy(r.BaseURL)
}

// listenLoopback binds a free loopback port. The listener stays bound until the server serves on it: closing it
// to reuse its port would let another test (or an outgoing connection) take the port before the server binds it.
func listenLoopback() (net.Listener, int, error) {
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, 0, fmt.Errorf("listen on loopback: %w", err)
	}

	tcpAddr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		_ = listener.Close()

		return nil, 0, fmt.Errorf("%w: %T", errUnexpectedListenerAddr, listener.Addr())
	}

	return listener, tcpAddr.Port, nil
}

// publish writes the objects and the manifest of the flag set to the file store of the relay.
func (r *Relay) publish(flagsYAML string) error {
	// the engine serves a flag set whose flags it rejected, as an empty one: refuse a file that is not YAML here
	var flags map[string]any

	err := yaml.Unmarshal([]byte(flagsYAML), &flags)
	if err != nil {
		return fmt.Errorf("the flag file of the relay is not YAML: %w", err)
	}

	store := filepath.Join(r.dir, storeDir)

	put := func(key string, data []byte) error {
		path := filepath.Join(store, key)

		dirErr := os.MkdirAll(filepath.Dir(path), 0o750)
		if dirErr != nil {
			return fmt.Errorf("create the store directory: %w", dirErr)
		}

		writeErr := os.WriteFile(path, data, 0o600)
		if writeErr != nil {
			return fmt.Errorf("write the store object: %w", writeErr)
		}

		return nil
	}

	doc, err := controlplane.MarshalFlagSet(&controlplane.FlagSetDocument{FlagSets: []controlplane.FlagSet{{
		Name: setName, Sources: []string{flagsObject}, APIKeys: []string{APIKey},
	}}})
	if err != nil {
		return fmt.Errorf("encode the flag sets: %w", err)
	}

	manifest := &controlplane.Manifest{
		APIVersion: controlplane.APIVersion, Kind: controlplane.DocumentKindManifest, Channel: channelName,
		Generation: 1,
	}

	for _, object := range []struct {
		name, kind string
		data       []byte
	}{
		{setsObject, controlplane.KindFlagSets, doc},
		{flagsObject, controlplane.KindFlags, []byte(flagsYAML)},
	} {
		sum := controlplane.ChecksumOf(object.data)

		key, keyErr := controlplane.ObjectKey(sum, controlplane.FormatYAML)
		if keyErr != nil {
			return fmt.Errorf("name the store object: %w", keyErr)
		}

		err = put(key, object.data)
		if err != nil {
			return err
		}

		manifest.Objects = append(manifest.Objects, controlplane.ManifestObject{
			Name: object.name, Kind: object.kind, Connection: connName, Key: key, Format: controlplane.FormatYAML,
			Checksum: sum, Size: int64(len(object.data)),
		})
	}

	data, err := controlplane.MarshalManifest(manifest)
	if err != nil {
		return fmt.Errorf("encode the manifest: %w", err)
	}

	return put(manifestKey, data)
}

// bootstrapConfig is the version 2 file of the relay: the file store, and the webhook exporter when the options ask
// for one.
func (r *Relay) bootstrapConfig(opts Options) (*bootstrap.Config, error) {
	doc := "version: 2\nconnections:\n  " + connName + ": {type: file, path: " + strconv.Quote(
		filepath.Join(r.dir, storeDir),
	) + "}\n" +
		"controlPlane:\n  connection: " + connName + "\n  manifest: {key: " + manifestKey + ", channel: " +
		channelName + ", jitter: 0}\n"

	if opts.ExporterMode != "" {
		r.sink = httptest.NewServer(r.sinkHandler(opts.ExporterMode))

		flush := opts.FlushIntervalMs
		if flush == 0 {
			flush = 1000
		}

		maxMem := opts.MaxEventInMemory
		if maxMem == 0 {
			maxMem = 100000
		}

		doc += fmt.Sprintf(
			"exporters:\n  sink: {kind: webhook, endpointUrl: {fromEnv: %s}, flushInterval: %q, maxEventInMemory: %d}\n",
			sinkEnv,
			time.Duration(flush)*time.Millisecond,
			maxMem,
		)
	}

	cfg, err := bootstrap.Parse([]byte(doc), nil, r.secrets())
	if err != nil {
		return nil, fmt.Errorf("parse the relay bootstrap file: %w", err)
	}

	return cfg, nil
}

// checkServed evaluates a flag of the flag set: a corrupted object degrades its flag set only (the generation is
// applied without it), and a set that is routed but pending serves no flag.
func (r *Relay) checkServed() error {
	flagSet, err := r.FlagSets.FlagSet(APIKey)
	if err != nil {
		return fmt.Errorf("%w: the flag set is not served: %w", errNotReady, err)
	}

	key := loadtest.FlagKey(loadtest.Scenarios()[0], loadtest.Types()[0], false)

	_, err = flagSet.RawVariation(key, ffcontext.NewEvaluationContext("readiness"), nil)
	if err != nil {
		return fmt.Errorf("%w: the flag %s is not served: %w", errNotReady, key, err)
	}

	return nil
}

// secrets reads the address of the sink from a variable that only the relay sees, not from the process environment.
func (r *Relay) secrets() bootstrap.LoadOptions {
	return bootstrap.LoadOptions{LookupEnv: func(name string) (string, bool) {
		if name == sinkEnv && r.sink != nil {
			return r.sink.URL, true
		}

		return "", false
	}}
}

// waitApplied waits for the engine to apply the first generation, and fails when it rejects it (a flag file that is
// not valid).
func waitApplied(rounds <-chan flagruntime.RoundResult) error {
	timeout := time.After(readyTimeout)

	for {
		select {
		case res := <-rounds:
			switch {
			case res.Generation != 1:
				// the bootstrap round, before the first manifest
			case res.Outcome == flagruntime.OutcomeApplied:
				return nil
			case res.Outcome == flagruntime.OutcomeRejected:
				return fmt.Errorf("%w: the engine rejected the flags", errNotReady)
			}
		case <-timeout:
			return fmt.Errorf("%w: no generation was applied in %s", errNotReady, readyTimeout)
		}
	}
}

func (r *Relay) sinkHandler(mode loadtest.ExporterMode) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		n, _ := io.Copy(io.Discard, req.Body)

		r.SinkRequests.Add(1)
		r.SinkBytes.Add(n)

		switch mode {
		case loadtest.ExporterFailing:
			w.WriteHeader(http.StatusInternalServerError)
		case loadtest.ExporterSlow:
			select {
			case <-time.After(loadtest.SlowDelay):
			case <-req.Context().Done():
				return
			}

			w.WriteHeader(http.StatusOK)
		case loadtest.ExporterDiscard:
			w.WriteHeader(http.StatusOK)
		}
	}
}

// healthRequestTimeout bounds one health request.
const healthRequestTimeout = 5 * time.Second

// waitHealthy polls the relay proxy health endpoint until it answers or 15s elapse.
func waitHealthy(baseURL string) error {
	client := &http.Client{Timeout: healthRequestTimeout}
	deadline := time.Now().Add(readyTimeout)

	for {
		var resp *http.Response

		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, baseURL+"/health", http.NoBody)
		if err == nil {
			resp, err = client.Do(req)
		}

		if err == nil {
			_ = resp.Body.Close()

			return nil
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("relay proxy did not start: %w", err)
		}

		time.Sleep(readyPollInterval)
	}
}

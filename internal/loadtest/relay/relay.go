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
	"sync/atomic"
	"time"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service/stream"
	"github.com/blackbirdworks/semaphore/cmdhelpers/log"
	"github.com/blackbirdworks/semaphore/cmdhelpers/retrieverconf"
	"github.com/blackbirdworks/semaphore/internal/loadtest"
	"github.com/blackbirdworks/semaphore/notifier"
)

const (
	// readyTimeout is how long the relay proxy has to answer the health check, polled every readyPollInterval.
	readyTimeout      = 15 * time.Second
	readyPollInterval = 20 * time.Millisecond
)

var errUnexpectedListenerAddr = errors.New("unexpected listener address type")

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
	// H2C makes the relay proxy accept HTTP/2 over cleartext (server.enableH2C); only with Listen.
	H2C bool
	// Listen enables the real HTTP server on a free loopback port (needed for the soak).
	Listen bool
}

// Relay is a running relay proxy.
type Relay struct {
	wsSvc  stream.WebsocketService
	sink   *httptest.Server
	server *api.Server
	// BaseURL is the loopback URL of the HTTP server (only if Options.Listen).
	BaseURL string
	dir     string
	// Services are the relay proxy services, usable to build handlers in-process.
	Services service.Services
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

	conf, err := buildRelayConfig(r, opts)
	if err != nil {
		return nil, err
	}

	var (
		baseURL  string
		listener net.Listener
	)
	defer func() {
		if err != nil && listener != nil {
			_ = listener.Close()
		}
	}()

	if opts.Listen {
		// The listener stays bound until the server serves on it: closing it to reuse its port would let
		// another test (or an outgoing connection) take the port before the server binds it.
		listener, err = (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
		if err != nil {
			return nil, fmt.Errorf("listen on loopback: %w", err)
		}

		tcpAddr, ok := listener.Addr().(*net.TCPAddr)
		if !ok {
			return nil, fmt.Errorf("%w: %T", errUnexpectedListenerAddr, listener.Addr())
		}

		port := tcpAddr.Port
		conf.Server.Port = port
		baseURL = fmt.Sprintf("http://127.0.0.1:%d", port)
	}

	logger := log.NewDefault()

	metrics, err := metric.NewMetrics()
	if err != nil {
		return nil, fmt.Errorf("init relay metrics: %w", err)
	}

	r.wsSvc = stream.NewWebsocketService()

	flagsetMgr, err := service.NewFlagsetManager(conf, logger.Slog,
		[]notifier.Notifier{metric.NewPrometheusNotifier(metrics)}, nil, r.wsSvc)
	if err != nil {
		return nil, fmt.Errorf("init flagset manager: %w", err)
	}

	r.Services = service.Services{
		MonitoringService: service.NewMonitoring(flagsetMgr),
		WebsocketService:  r.wsSvc,
		FlagsetManager:    flagsetMgr,
		Metrics:           metrics,
	}
	if opts.Listen {
		s := api.New(conf, r.Services, logger.Slog, api.WithListener(listener))

		r.server = &s
		go s.StartWithContext(context.Background())
		// The server owns the listener now: it closes it when it stops, the error path must only stop the server.
		listener = nil
		r.BaseURL = baseURL

		waitHealthyErr := waitHealthy(baseURL)
		if waitHealthyErr != nil {
			return nil, waitHealthyErr
		}
	}

	return r, nil
}

func buildRelayConfig(r *Relay, opts Options) (*config.Config, error) {
	flagFile := filepath.Join(r.dir, "flags.yaml")

	err := os.WriteFile(flagFile, []byte(opts.FlagsYAML), 0o600)
	if err != nil {
		return nil, fmt.Errorf("write relay flag file: %w", err)
	}

	conf := &config.Config{
		Retrievers: &[]retrieverconf.RetrieverConf{{Kind: "file", Path: flagFile}},
		// the server (and its monitoring port) only listens on the loopback interface
		Server: config.Server{
			Mode:                  config.ServerModeHTTP,
			Host:                  "127.0.0.1",
			UnixSocketPath:        "",
			LambdaAdapter:         "",
			AwsAPIGatewayBasePath: "",
			Port:                  0,
			MonitoringPort:        0,
			EnableH2C:             opts.H2C,
		},
	}
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

		conf.Exporter = &config.ExporterConf{
			Kind:             config.WebhookExporter,
			EndpointURL:      r.sink.URL,
			FlushInterval:    flush,
			MaxEventInMemory: maxMem,
		}
	}

	return conf, nil
}

// Dir returns the temporary directory holding the flag file (removed by Close).
func (r *Relay) Dir() string { return r.dir }

// Close stops the relay proxy and removes its temporary flag file.
func (r *Relay) Close() {
	if r.server != nil {
		r.server.Stop(context.Background())
	}

	if r.Services.FlagsetManager != nil {
		r.Services.FlagsetManager.Close()
	}

	if r.wsSvc != nil {
		r.wsSvc.Close()
	}

	if r.sink != nil {
		r.sink.CloseClientConnections()
		r.sink.Close()
	}

	if r.dir != "" {
		_ = os.RemoveAll(r.dir)
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

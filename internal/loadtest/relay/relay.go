// Package relay starts an in-process relay proxy for the benchmarks and the soak harness.
package relay

import (
	"context"
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

// Options configures Start.
type Options struct {
	// FlagsYAML is the flag configuration (see loadtest.FlagsYAML).
	FlagsYAML string
	// ExporterMode is the behaviour of the webhook sink the relay proxy exports to.
	// An empty value disables the exporter.
	ExporterMode loadtest.ExporterMode
	// FlushIntervalMs is the flush interval of the exporter (default 1000).
	FlushIntervalMs int64
	// MaxEventInMemory is the flush trigger of the exporter (default 100000).
	MaxEventInMemory int64
	// TempDir is the directory the flag file directory is created in (default: os.TempDir()).
	TempDir string
	// Listen enables the real HTTP server on a free loopback port (needed for the soak).
	Listen bool
}

// Relay is a running relay proxy.
type Relay struct {
	// BaseURL is the loopback URL of the HTTP server (only if Options.Listen).
	BaseURL string
	// Services are the relay proxy services, usable to build handlers in-process.
	Services service.Services
	// SinkRequests counts the requests received by the webhook sink.
	SinkRequests atomic.Int64
	// SinkBytes counts the bytes received by the webhook sink.
	SinkBytes atomic.Int64

	sink   *httptest.Server
	server *api.Server
	wsSvc  stream.WebsocketService
	dir    string
}

// Start starts a relay proxy.
func Start(opts Options) (_ *Relay, err error) {
	dir, err := os.MkdirTemp(opts.TempDir, "ffrelay")
	if err != nil {
		return nil, err
	}
	r := &Relay{dir: dir}
	defer func() {
		if err != nil {
			r.Close() // releases whatever was started and removes the temp dir
		}
	}()
	flagFile := filepath.Join(dir, "flags.yaml")
	if err = os.WriteFile(flagFile, []byte(opts.FlagsYAML), 0o600); err != nil {
		return nil, err
	}
	conf := &config.Config{
		CommonFlagSet: config.CommonFlagSet{
			Retrievers: &[]retrieverconf.RetrieverConf{{Kind: "file", Path: flagFile}},
		},
		// the server (and its monitoring port) only listens on the loopback interface
		Server: config.Server{Mode: config.ServerModeHTTP, Host: "127.0.0.1"},
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
	var baseURL string
	if opts.Listen {
		l, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		port := l.Addr().(*net.TCPAddr).Port
		_ = l.Close()
		conf.Server.Port = port
		baseURL = fmt.Sprintf("http://127.0.0.1:%d", port)
	}

	logger := log.InitLogger()
	metrics, err := metric.NewMetrics()
	if err != nil {
		return nil, err
	}
	r.wsSvc = stream.NewWebsocketService()
	fm, err := service.NewFlagsetManager(conf, logger.ZapLogger,
		[]notifier.Notifier{metric.NewPrometheusNotifier(metrics)}, nil, r.wsSvc)
	if err != nil {
		return nil, err
	}
	r.Services = service.Services{
		MonitoringService: service.NewMonitoring(fm),
		WebsocketService:  r.wsSvc,
		FlagsetManager:    fm,
		Metrics:           metrics,
	}
	if opts.Listen {
		s := api.New(conf, r.Services, logger.ZapLogger)
		r.server = &s
		go s.StartWithContext(context.Background())
		r.BaseURL = baseURL
		if err := waitHealthy(baseURL); err != nil {
			return nil, err
		}
	}
	return r, nil
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
		default:
			w.WriteHeader(http.StatusOK)
		}
	}
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

// waitHealthy polls the relay proxy health endpoint until it answers or 15s elapse.
func waitHealthy(baseURL string) error {
	deadline := time.Now().Add(15 * time.Second)
	for {
		var resp *http.Response
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, baseURL+"/health", nil)
		if err == nil {
			resp, err = http.DefaultClient.Do(req)
		}
		if err == nil {
			_ = resp.Body.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("relay proxy did not start: %w", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

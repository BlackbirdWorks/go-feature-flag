package corehttp

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
)

// ErrNoStreams is returned by [New] when the configuration has no streams: the server does not make services
// (they run goroutines) that nobody owns.
var ErrNoStreams = errors.New("corehttp: the configuration has no streams")

// Config is what the server needs besides the core.
type Config struct {
	// Settings are the settings of the HTTP layer.
	Settings *api.Settings
	// Logger is the logger of the server; nil discards the logs.
	Logger *slog.Logger
	// Streams serves the streams of flag changes, it is required. To get changes it must also be the Broadcast of
	// the core. The caller owns it and closes it ([Streams.Close]) after the server stopped.
	Streams *Streams
	// Metrics are the metrics of the handlers; nil makes them. The server registers the collector of the config
	// status ([NewConfigCollector]) in its registry.
	Metrics *metric.Metrics
	// Readiness is the readiness the probes answer from. The caller keeps it to call [Readiness.BeginShutdown] first
	// on SIGTERM, before the listeners close; nil makes one that follows the core only ([relaycore.Status]).
	Readiness *Readiness
}

// New builds the HTTP server of a core. The server serves the evaluation API, the flag configuration, the flag change,
// the collection of events and the streams from the runtime of the core. The caller starts it (Start) and stops it
// before the core.
func New(src Source, cfg *Config, opts ...api.Option) (api.Server, error) {
	adapter := NewAdapter(src)

	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}

	streams := cfg.Streams
	if streams == nil {
		return api.Server{}, ErrNoStreams
	}

	metrics := cfg.Metrics
	if metrics == nil {
		made, err := metric.NewMetrics()
		if err != nil {
			return api.Server{}, fmt.Errorf("making the metrics: %w", err)
		}

		metrics = made
	}

	ready := cfg.Readiness
	if ready == nil {
		ready = NewReadiness(src)
	}

	if metrics.Enabled() {
		err := metrics.Registry.Register(NewConfigCollectorSharing(src, ready))
		if err != nil {
			return api.Server{}, fmt.Errorf("registering the config metrics: %w", err)
		}

		err = metrics.Registry.Register(NewSegmentCollector(src))
		if err != nil {
			return api.Server{}, fmt.Errorf("registering the segment metrics: %w", err)
		}
	}

	services := &service.Services{
		MonitoringService: monitoring{Readiness: ready, holder: src.Holder()},
		WebsocketService:  streams.ws,
		SSEService:        streams.sse,
		Metrics:           metrics,
	}

	all := append([]api.Option{api.WithFlagSetResolver(adapter)}, opts...)

	return api.New(cfg.Settings, service.NewAPIKeysFrom(adapter), services, logger, all...), nil
}

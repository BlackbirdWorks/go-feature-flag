package corehttp

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/model"
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
	// Metrics are the metrics of the handlers; nil makes them.
	Metrics *metric.Metrics
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

	services := &service.Services{
		MonitoringService: monitoring{holder: src.Holder()},
		WebsocketService:  streams.ws,
		SSEService:        streams.sse,
		Metrics:           metrics,
	}

	all := append([]api.Option{api.WithFlagSetResolver(adapter)}, opts...)

	return api.New(cfg.Settings, service.NewAPIKeysFrom(adapter), services, logger, all...), nil
}

// monitoring answers the health and the info endpoints from the runtime.
type monitoring struct {
	holder *flagruntime.Holder
}

// Health reports that the server is initialized.
func (monitoring) Health() model.HealthResponse { return model.HealthResponse{Initialized: true} }

// Info lists the flag sets with the date of their last change.
func (m monitoring) Info() (model.InfoResponse, error) {
	rt := m.holder.Load()
	dates := make(map[string]time.Time, len(rt.FlagSets))

	var latest time.Time

	for name, set := range rt.FlagSets {
		dates[name] = set.ModifiedAt
		if set.ModifiedAt.After(latest) {
			latest = set.ModifiedAt
		}
	}

	return model.InfoResponse{Flagsets: dates, LatestCacheRefresh: &latest}, nil
}

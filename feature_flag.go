package ffclient

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/internal/cache"
	"github.com/blackbirdworks/semaphore/internal/notification"
	"github.com/blackbirdworks/semaphore/modules/core/flag"
	"github.com/blackbirdworks/semaphore/notifier/logsnotifier"
	"github.com/blackbirdworks/semaphore/retriever"
	"github.com/blackbirdworks/semaphore/utils/fflog"
)

// GoFeatureFlag is the main object of the library
// it contains the cache, the config, the updater and the exporter.
type GoFeatureFlag struct {
	trackingEventDataExporter exporter.Manager[exporter.TrackingEvent]
	featureEventDataExporter  exporter.Manager[exporter.FeatureEvent]
	retrieverManager          *retriever.Manager
	// eventWorkerStop is closed by Close to ask the worker to drain the queue and exit.
	eventWorkerStop chan struct{}
	// evaluationService owns the caches of the flag evaluation (parsed queries, scheduled rollouts, ...).
	// It is created with the client, shared by the cache that validates the flags and by the
	// evaluations, and released by Close.
	evaluationService *flag.Service
	// eventQueue is the bounded queue between the evaluations and the goroutine that adds the
	// events in the exporter, so that an evaluation never blocks on (nor spawns a goroutine for)
	// the exporter. It is nil when there is no exporter for the evaluation events.
	eventQueue chan exporter.FeatureEvent
	// enrichment is the evaluation context enrichment (with the env), built once by evaluationEnrichment.
	enrichment map[string]any
	// dropSignal asks reportDrops to write the warning about the dropped events.
	dropSignal chan struct{}
	config     Config
	// evalExporterWg waits for the event worker to finish before closing GOFF
	evalExporterWg sync.WaitGroup
	// droppedEvents counts the evaluation events dropped because eventQueue was full.
	droppedEvents  atomic.Int64
	eventAdmission sync.RWMutex
	closeOnce      sync.Once
	enrichmentOnce sync.Once
	// closing is set by Close, under eventAdmission, before the event workers are asked to stop: an
	// event is only put in eventQueue while it is false, so that nothing is queued after the final drain.
	closing bool
}

// DefaultEventQueueSize is the default value of Config.EventQueueSize.
const DefaultEventQueueSize = 65536

// New creates a new semaphore instances that retrieve the config from a YAML file
// and return everything you need to manage your flags.
func New(config Config) (*GoFeatureFlag, error) {
	return NewWithContext(context.Background(), config)
}

// NewWithContext creates a new semaphore instance using ctx to initialize the configured retrievers.
//
//nolint:contextcheck // defensive fallback when a caller passes a nil context
func NewWithContext(ctx context.Context, config Config) (*GoFeatureFlag, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	config.Initialize()

	goFF := &GoFeatureFlag{
		config:            config,
		evalExporterWg:    sync.WaitGroup{},
		evaluationService: flag.NewService(flag.WithLogger(config.serviceLogger())),
	}

	if config.Offline {
		// in case we are in offline mode, we don't need to initialize the cache since we will not use it.
		goFF.config.internalLogger.Info("Semaphore is in offline mode")

		return goFF, nil
	}

	retrieverManager, err := initializeRetrieverManager(ctx, config, goFF.evaluationService)
	if err != nil && (goFF.retrieverManager == nil || !config.StartWithRetrieverError) {
		return nil, fmt.Errorf(
			"impossible to initialize the retrievers, please check your configuration: %w",
			err,
		)
	}
	goFF.retrieverManager = retrieverManager
	goFF.featureEventDataExporter, goFF.trackingEventDataExporter = initializeDataExporters(
		config, goFF.config.internalLogger)
	goFF.startEventWorker()
	config.internalLogger.Debug("Semaphore is initialized")

	return goFF, nil
}

// initializeNotificationService is a function that will initialize the notification service with the notifiers.
func initializeNotificationService(config Config) *notification.ServiceImpl {
	notifiers := config.Notifiers
	notifiers = append(notifiers, &logsnotifier.Notifier{Logger: config.internalLogger})

	return notification.NewService(notifiers)
}

// initializeRetrieverManager is a function that will initialize the retriever manager with the retrievers.
func initializeRetrieverManager(
	ctx context.Context, config Config, evaluationService *flag.Service,
) (*retriever.Manager, error) {
	retrievers, err := config.GetRetrievers()
	if err != nil {
		return nil, err
	}
	mngrConfig := retriever.ManagerConfig{
		FileFormat:                      config.FileFormat,
		DisableNotifierOnInit:           config.DisableNotifierOnInit,
		PersistentFlagConfigurationFile: config.PersistentFlagConfigurationFile,
		StartWithRetrieverError:         config.StartWithRetrieverError,
		EnablePollingJitter:             config.EnablePollingJitter,
		PollingInterval:                 config.PollingInterval,
		Name:                            config.Name,
	}

	notificationService := initializeNotificationService(config)
	// init internal cache
	cacheMngr := cache.New(
		notificationService,
		config.PersistentFlagConfigurationFile,
		config.internalLogger,
		cache.WithService(evaluationService),
	)

	manager := retriever.NewManager(mngrConfig, retrievers, cacheMngr, config.internalLogger)
	err = manager.Init(ctx)

	return manager, err
}

func initializeDataExporters(config Config, logger *fflog.FFLogger) (
	exporter.Manager[exporter.FeatureEvent], exporter.Manager[exporter.TrackingEvent],
) {
	exporters := config.GetDataExporters()
	featureEventExporterConfigs := make([]exporter.Config, 0)
	trackingEventExporterConfigs := make([]exporter.Config, 0)
	if len(exporters) > 0 {
		for _, exp := range exporters {
			c := exporter.Config{
				Exporter:         exp.Exporter,
				FlushInterval:    exp.FlushInterval,
				MaxEventInMemory: exp.MaxEventInMemory,
				MaxQueuedEvents:  exp.MaxQueuedEvents,
			}
			if exp.ExporterEventType == TrackingEventExporter {
				trackingEventExporterConfigs = append(trackingEventExporterConfigs, c)

				continue
			}
			featureEventExporterConfigs = append(featureEventExporterConfigs, c)
		}
	}

	var trackingEventManager exporter.Manager[exporter.TrackingEvent]
	if len(trackingEventExporterConfigs) > 0 {
		trackingEventManager = exporter.NewManager[exporter.TrackingEvent](
			trackingEventExporterConfigs, config.ExporterCleanQueueInterval, logger)
		trackingEventManager.Start()
	}

	var featureEventManager exporter.Manager[exporter.FeatureEvent]
	if len(featureEventExporterConfigs) > 0 {
		featureEventManager = exporter.NewManager[exporter.FeatureEvent](
			featureEventExporterConfigs, config.ExporterCleanQueueInterval, logger)
		featureEventManager.Start()
	}

	return featureEventManager, trackingEventManager
}

// Close wait until thread are done.
// The events of the evaluations that finish while Close runs are exported by its final flush; an
// evaluation that finishes after Close has returned has nowhere to send its event.
func (g *GoFeatureFlag) Close() {
	g.CloseWithContext(context.Background())
}

// CloseWithContext waits until background workers are done, passing ctx to retriever shutdown.
// The events of the evaluations that finish while CloseWithContext runs are exported by its final flush;
// an evaluation that finishes after CloseWithContext has returned has nowhere to send its event.
//
//nolint:contextcheck // defensive fallback when a caller passes a nil context
func (g *GoFeatureFlag) CloseWithContext(ctx context.Context) {
	if g == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if g.retrieverManager != nil {
		_ = g.retrieverManager.Shutdown(ctx)
	}
	// we have to wait for the event worker (it drains the queue) before stopping the exporter
	g.closeOnce.Do(func() {
		// wait for the evaluations that are putting their event in the queue, and refuse the next ones
		g.eventAdmission.Lock()
		g.closing = true
		g.eventAdmission.Unlock()
		if g.eventWorkerStop != nil {
			close(g.eventWorkerStop)
		}
	})
	g.evalExporterWg.Wait()
	if g.featureEventDataExporter != nil {
		g.featureEventDataExporter.Stop()
	}

	if g.trackingEventDataExporter != nil {
		g.trackingEventDataExporter.Stop()
	}
	g.evaluationService.Close()
}

// GetCacheRefreshDate gives the last refresh date of the cache.
func (g *GoFeatureFlag) GetCacheRefreshDate() time.Time {
	if g.IsOffline() {
		return time.Time{}
	}

	return g.retrieverManager.GetCacheRefreshDate()
}

// EvaluationService returns the evaluation service of the client: the owner of the caches that
// evaluating (and validating) its flags uses. Pass it in flag.Context.Service when you evaluate the
// flags that GetFlagsFromCache returns. It is never nil.
func (g *GoFeatureFlag) EvaluationService() *flag.Service {
	return g.evaluationService
}

// GetEvaluationContextEnrichment returns the evaluation context enrichment.
func (g *GoFeatureFlag) GetEvaluationContextEnrichment() map[string]any {
	return g.config.EvaluationContextEnrichment
}

// ForceRefresh is a function that forces to call the retrievers and refresh the configuration of flags.
// This function can be called explicitly to refresh the flags if you know that a change has been made in
// the configuration.
func (g *GoFeatureFlag) ForceRefresh() bool {
	return g.ForceRefreshWithContext(context.Background())
}

// ForceRefreshWithContext is a function that forces to call the retrievers and refresh the configuration of flags.
// This function can be called explicitly to refresh the flags if you know that a change has been made in
// the configuration.
func (g *GoFeatureFlag) ForceRefreshWithContext(ctx context.Context) bool {
	if g.IsOffline() {
		return false
	}

	return g.retrieverManager.ForceRefresh(ctx)
}

// SetOffline updates the config Offline parameter.
func (g *GoFeatureFlag) SetOffline(control bool) {
	g.config.SetOffline(control)
}

// SetOfflineWithContext updates the config Offline parameter, and stops (going offline) or restarts
// (going back online) the background polling of the retrievers.
//
// Known limitation: going back online does not restart the background polling. StopPolling
// closes the background updater channel for good, so the StartPolling call below returns
// immediately. Fixing this requires recreating the background updater, which is tracked
// separately.
func (g *GoFeatureFlag) SetOfflineWithContext(ctx context.Context, control bool) {
	if !g.IsOffline() && control {
		g.retrieverManager.StopPolling()
	}
	if g.IsOffline() && !control {
		g.retrieverManager.StartPolling(ctx)
	}
	g.SetOffline(control)
}

// IsOffline allows knowing if the feature flag is in offline mode.
func (g *GoFeatureFlag) IsOffline() bool {
	return g.config.IsOffline()
}

// GetPollingInterval is the polling interval between two refreshes of the cache.
func (g *GoFeatureFlag) GetPollingInterval() int64 {
	return g.config.PollingInterval.Milliseconds()
}

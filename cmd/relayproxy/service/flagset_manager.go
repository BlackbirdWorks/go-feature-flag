package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"reflect"
	"slices"
	"sync"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/google/uuid"

	ffclient "github.com/blackbirdworks/semaphore"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/proxynotifier"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service/stream"
	cmdlog "github.com/blackbirdworks/semaphore/cmdhelpers/log"
	"github.com/blackbirdworks/semaphore/notifier"
	"github.com/blackbirdworks/semaphore/retriever/postgresqlretriever"
	"github.com/blackbirdworks/semaphore/utils"
)

const (
	// extraNotifiers is the room reserved for the notifiers the relay proxy adds to the configured ones.
	extraNotifiers = 2
)

var (
	errConfigurationIsNil         = errors.New("configuration is nil")
	errNoFlagsetConfigured        = errors.New("no flagset configured")
	errNoAPIKeyProvided           = errors.New("no API key provided")
	errFlagsetNotFoundFor         = errors.New("flagset not found for API key")
	errImpossibleToFindThe        = errors.New("impossible to find the flagset with the name")
	errNoConfiguredFlagset        = errors.New("no configured flagset")
	errAPIKeyIsRequiredWhen       = errors.New("apiKey is required when using flagsets")
	errNoFlagSetAssociated        = errors.New("no flag set associated to the API key")
	errNoFlagsetsConfigured       = errors.New("no flagsets configured")
	errNoDefaultFlagsetConfigured = errors.New("no default flagset configured")
)

type flagsetManagerMode string

const (
	flagsetManagerModeDefault  flagsetManagerMode = "default"
	flagsetManagerModeFlagsets flagsetManagerMode = "flagsets"
)

// FlagsetManager is the interface for managing flagsets.
// It is used to retrieve the flagset linked to the API Key.
type FlagsetManager interface {
	// FlagSet returns the flag set linked to the API Key
	FlagSet(apiKey string) (*ffclient.GoFeatureFlag, error)
	// FlagSetName returns the name of the flagset linked to the API Key.
	// In default mode it returns the default flagset name, with flagsets it returns an
	// error if the API Key is empty or not linked to a flagset.
	FlagSetName(apiKey string) (string, error)
	// AllFlagSets returns all flag sets of the flagset manager
	AllFlagSets() (map[string]*ffclient.GoFeatureFlag, error)
	// Default returns the default flagset
	Default() *ffclient.GoFeatureFlag
	// IsDefaultFlagSet returns true if the manager is in default mode (no flagsets configured)
	IsDefaultFlagSet() bool
	// Close closes the flagset manager
	Close()
	// OnConfigChange is called when the configuration changes
	OnConfigChange(newConfig *config.Config)
}

// FlagsetManagerImpl is the internal implementation of FlagsetManager.
type FlagsetManagerImpl struct {
	// sseService is used to create a per-flagset SSE notifier when a flagset is added at
	// runtime. It can be nil.
	sseService stream.SSEService
	// wsService is used to create a per-flagset websocket notifier when a flagset is added at
	// runtime. It can be nil.
	wsService stream.WebsocketService
	// Logger is the logger for the flagset manager.
	logger *slog.Logger
	// FlagSetRouting routes a request credential to the NAME of the flagset it selects (the values are
	// public flagset names, the keys are secrets and must never be logged).
	// It is used to retrieve the flagset linked to the API Key.
	FlagSetRouting map[string]string
	// FlagSets is a map that stores the different instances of GoFeatureFlag (one per flagset)
	// It is used to retrieve the flagset linked to the API Key.
	FlagSets map[string]*ffclient.GoFeatureFlag
	// Config is the configuration of the relay proxy.
	// It is used to retrieve the configuration of the relay proxy.
	config *config.Config
	// DefaultFlagSet is the flagset used when no API Key is provider.
	// It is the legacy way to handle feature flags in Semaphore.
	// This is used only if no flag set is configured in the configuration file.
	DefaultFlagSet *ffclient.GoFeatureFlag
	// sharedRegistry shares the S3 requests of the flagsets using enableSharedPollerPerBucket.
	sharedRegistry *sharedBucketRegistry
	// postgresPools shares the PostgreSQL connection pools between the flagsets that use the same database.
	postgresPools *postgresqlretriever.PoolRegistry
	// Mode is the mode of the flagset manager.
	mode flagsetManagerMode
	// notifiers are the relay-proxy level notifiers (e.g. prometheus) passed at
	// construction time. They are stored so we can re-create a GoFeatureFlag client when a
	// flagset is added at runtime.
	notifiers []notifier.Notifier
	// clientOptions are extra options for the clients created at runtime (nil outside the tests).
	clientOptions []clientOption
	// flagsetsMutex protects concurrent access to FlagSets and FlagSetRouting.
	// Both maps can be mutated at runtime when flagsets are added or removed, so every
	// read or write of these maps must be done while holding this lock.
	flagsetsMutex sync.RWMutex
}

// NewFlagsetManager is creating a new FlagsetManagerImpl.
// It is used to retrieve the flagset linked to the API Key.
// sseService and wsService are optional: when non-nil a per-flagset SSE / websocket
// notifier is created so that flag-change events are scoped to the correct flagset.
func NewFlagsetManager(
	cfg *config.Config, logger *slog.Logger, notifiers []notifier.Notifier,
	sseService stream.SSEService, wsService stream.WebsocketService,
) (*FlagsetManagerImpl, error) {
	if cfg == nil {
		return nil, errConfigurationIsNil
	}

	var (
		flagsetMngr *FlagsetManagerImpl
		err         error
	)
	if cfg.IsUsingFlagsets() {
		flagsetMngr, err = newFlagsetManagerWithFlagsets(cfg, logger, notifiers, sseService, wsService)
	} else {
		flagsetMngr, err = newFlagsetManagerWithDefaultConfig(cfg, logger, notifiers, sseService, wsService)
	}

	if err != nil {
		return nil, err
	}

	cfg.AttachConfigChangeCallback(flagsetMngr.OnConfigChange)

	return flagsetMngr, nil
}

func newFlagsetManagerWithDefaultConfig(
	cfg *config.Config, logger *slog.Logger, notifiers []notifier.Notifier,
	sseService stream.SSEService, wsService stream.WebsocketService,
) (*FlagsetManagerImpl, error) {
	defaultFlagSet := config.FlagSet{
		Name:                            utils.DefaultFlagSetName,
		Retriever:                       cfg.Retriever,
		Retrievers:                      cfg.Retrievers,
		Notifiers:                       cfg.Notifiers,
		Exporter:                        cfg.Exporter,
		Exporters:                       cfg.Exporters,
		FileFormat:                      cfg.FileFormat,
		PollingInterval:                 cfg.PollingInterval,
		ExporterCleanQueueInterval:      cfg.ExporterCleanQueueInterval,
		StartWithRetrieverError:         cfg.StartWithRetrieverError,
		EnablePollingJitter:             cfg.EnablePollingJitter,
		DisableNotifierOnInit:           cfg.DisableNotifierOnInit,
		EvaluationContextEnrichment:     cfg.EvaluationContextEnrichment,
		PersistentFlagConfigurationFile: cfg.PersistentFlagConfigurationFile,
	}
	allNotifiers := appendStreamNotifiers(notifiers, sseService, wsService, utils.DefaultFlagSetName)

	client, err := NewGoFeatureFlagClient(&defaultFlagSet, logger, allNotifiers)
	if err != nil {
		return nil, err
	}

	return &FlagsetManagerImpl{
		DefaultFlagSet: client,
		notifiers:      notifiers,
		sseService:     sseService,
		wsService:      wsService,
		config:         cfg,
		mode:           flagsetManagerModeDefault,
		logger:         logger,
	}, nil
}

func newFlagsetManagerWithFlagsets(
	cfg *config.Config, logger *slog.Logger, notifiers []notifier.Notifier,
	sseService stream.SSEService, wsService stream.WebsocketService,
) (*FlagsetManagerImpl, error) {
	return newFlagsetManagerWithFlagsetsAndRegistry(
		cfg, logger, notifiers, sseService, wsService, newSharedBucketRegistry(
			nil, time.Duration(cfg.PollingInterval)*time.Millisecond, cfg.EnablePollingJitter, logger))
}

const unroutableKeysWarning = "authorizedKeys.evaluation and apiKeys are ignored in flag set mode: " +
	"an evaluation key must be listed in the apiKeys of a flag set"

func newFlagsetManagerWithFlagsetsAndRegistry(
	cfg *config.Config, logger *slog.Logger, notifiers []notifier.Notifier,
	sseService stream.SSEService, wsService stream.WebsocketService,
	registry *sharedBucketRegistry,
) (*FlagsetManagerImpl, error) {
	if cfg.HasUnroutableEvaluationKeys() {
		logger.Warn(unroutableKeysWarning)
	}

	flagsets := make(map[string]*ffclient.GoFeatureFlag)
	apiKeysToFlagSet := make(map[string]string)
	postgresPools := postgresqlretriever.NewPoolRegistry()

	// Register all the shared-poller items first so that the first listing of a bucket covers all of them.
	registry.preRegisterAll(cfg.FlagSets)
	defer registry.releasePending()

	for index, flagset := range cfg.FlagSets {
		flagSetName := flagset.Name
		if !isNamedFlagset(flagset) {
			flagSetName = uuid.New().String()

			startLog := "no flagset name provided"
			if flagset.Name == utils.DefaultFlagSetName {
				startLog = "using 'default' as a flagset name"
			}

			logMessage := startLog + ", generating a default flagset name. This is not recommended. " +
				"Not having a flagset name will not allow you to change API Keys associated to the flagset during runtime."
			logger.LogAttrs(context.Background(), slog.LevelWarn, logMessage, slog.String("flagset", flagSetName))
		}

		allNotifiers := appendStreamNotifiers(notifiers, sseService, wsService, flagSetName)

		client, err := newGoFeatureFlagClient(
			&flagset, logger, allNotifiers, withSharedBucketRegistry(registry), withPostgresPools(postgresPools),
			withDefaultExporterCleanQueueInterval(cfg.ExporterCleanQueueInterval))
		if err != nil {
			logger.LogAttrs(context.Background(), slog.LevelError,
				"failed to create the Semaphore client for the flagset",
				slog.Int("flagset_index", index),
				slog.String("flagset", flagset.Name),
				cmdlog.Err(err),
			)

			continue
		}

		flagsets[flagSetName] = client
		for _, apiKey := range flagset.APIKeys {
			apiKeysToFlagSet[apiKey] = flagSetName
		}
	}

	if len(flagsets) == 0 {
		return nil, errNoFlagsetConfigured
	}

	return &FlagsetManagerImpl{
		FlagSets:       flagsets,
		FlagSetRouting: apiKeysToFlagSet,
		notifiers:      notifiers,
		sseService:     sseService,
		wsService:      wsService,
		config:         cfg,
		mode:           flagsetManagerModeFlagsets,
		logger:         logger,
		sharedRegistry: registry,
		postgresPools:  postgresPools,
	}, nil
}

// FlagSet is returning the flag set linked to the API Key.
func (m *FlagsetManagerImpl) FlagSet(apiKey string) (*ffclient.GoFeatureFlag, error) {
	switch m.mode {
	case flagsetManagerModeFlagsets:
		if apiKey == "" {
			return nil, errNoAPIKeyProvided
		}

		m.flagsetsMutex.RLock()
		flagsetName, exists := m.FlagSetRouting[apiKey]
		flagset, flagsetExists := m.FlagSets[flagsetName]
		m.flagsetsMutex.RUnlock()

		if !exists {
			return nil, errFlagsetNotFoundFor
		}

		if !flagsetExists {
			return nil, fmt.Errorf("%w %s", errImpossibleToFindThe, flagsetName)
		}

		return flagset, nil
	case flagsetManagerModeDefault:
		if m.DefaultFlagSet == nil {
			return nil, errNoConfiguredFlagset
		}

		return m.DefaultFlagSet, nil
	default:
		if m.DefaultFlagSet == nil {
			return nil, errNoConfiguredFlagset
		}

		return m.DefaultFlagSet, nil
	}
}

// FlagSetName returns the name of the flagset linked to the API Key.
func (m *FlagsetManagerImpl) FlagSetName(apiKey string) (string, error) {
	switch m.mode {
	case flagsetManagerModeFlagsets:
		if apiKey == "" {
			return "", errAPIKeyIsRequiredWhen
		}

		m.flagsetsMutex.RLock()
		name, ok := m.FlagSetRouting[apiKey]
		m.flagsetsMutex.RUnlock()

		if ok {
			return name, nil
		}

		return "", errNoFlagSetAssociated
	case flagsetManagerModeDefault:
		return utils.DefaultFlagSetName, nil
	default:
		return utils.DefaultFlagSetName, nil
	}
}

// AllFlagSets returns the flag sets of the flagset manager.
func (m *FlagsetManagerImpl) AllFlagSets() (map[string]*ffclient.GoFeatureFlag, error) {
	switch m.mode {
	case flagsetManagerModeFlagsets:
		m.flagsetsMutex.RLock()
		defer m.flagsetsMutex.RUnlock()

		if len(m.FlagSets) == 0 {
			return nil, errNoFlagsetsConfigured
		}
		// Return a shallow copy so callers can iterate safely even if flagsets are
		// added or removed concurrently.
		out := make(map[string]*ffclient.GoFeatureFlag, len(m.FlagSets))
		maps.Copy(out, m.FlagSets)

		return out, nil
	case flagsetManagerModeDefault:
		if m.DefaultFlagSet == nil {
			return nil, errNoDefaultFlagsetConfigured
		}

		return map[string]*ffclient.GoFeatureFlag{
			utils.DefaultFlagSetName: m.DefaultFlagSet,
		}, nil
	default:
		if m.DefaultFlagSet == nil {
			return nil, errNoDefaultFlagsetConfigured
		}

		return map[string]*ffclient.GoFeatureFlag{
			utils.DefaultFlagSetName: m.DefaultFlagSet,
		}, nil
	}
}

// Default returns the default flagset.
func (m *FlagsetManagerImpl) Default() *ffclient.GoFeatureFlag {
	return m.DefaultFlagSet
}

// IsDefaultFlagSet returns true if the manager is in default mode (no flagsets configured).
func (m *FlagsetManagerImpl) IsDefaultFlagSet() bool {
	return m.mode == flagsetManagerModeDefault
}

// Close closes the flagset manager.
func (m *FlagsetManagerImpl) Close() {
	if m.DefaultFlagSet != nil {
		m.DefaultFlagSet.Close()
	}
	// Snapshot the clients under the lock, then close them outside of it. Close() flushes the
	// exporters and can block, so holding the lock for every flush would needlessly block a
	// concurrent config reload for the whole teardown. This mirrors removeFlagset.
	m.flagsetsMutex.RLock()

	clients := make([]*ffclient.GoFeatureFlag, 0, len(m.FlagSets))
	for _, flagset := range m.FlagSets {
		clients = append(clients, flagset)
	}

	m.flagsetsMutex.RUnlock()

	for _, client := range clients {
		client.Close()
	}
}

// OnConfigChange is called when the configuration changes.
func (m *FlagsetManagerImpl) OnConfigChange(newConfig *config.Config) {
	err := newConfig.IsValid()
	if err != nil {
		m.logger.LogAttrs(
			context.Background(),
			slog.LevelError,
			"the new configuration is invalid, it will not be applied",
			cmdlog.Err(err),
		)
		m.logger.LogAttrs(
			context.Background(),
			slog.LevelDebug,
			"invalid configuration:",
			cmdlog.Err(err),
			slog.Any("newConfig", newConfig),
		)

		return
	}

	// dont allow to switch from default to flagsets mode (or the opposite) during runtime
	if (newConfig.IsUsingFlagsets() && m.mode == flagsetManagerModeDefault) ||
		(!newConfig.IsUsingFlagsets() && m.mode == flagsetManagerModeFlagsets) {
		m.logger.Error("switching from default to flagsets mode (or the opposite) is not supported during runtime")

		return
	}

	switch m.mode {
	case flagsetManagerModeDefault:
		m.onConfigChangeWithDefault(newConfig)
	case flagsetManagerModeFlagsets:
		m.onConfigChangeWithFlagsets(newConfig)
	}
}

// onConfigChangeWithFlagsets reconciles the running flagsets with the new configuration.
// In flagsets mode we support, without a restart:
//   - adding a new flagset (a new GoFeatureFlag client is created),
//   - removing a flagset (its GoFeatureFlag client is closed),
//   - changing the API keys of an existing flagset,
//   - changing the authorizedKeys (admin keys).
//
// Any other modification of an existing flagset (retriever, exporter, notifier, polling
// interval, ...) is NOT supported and is rejected with an error: the change is ignored at
// runtime and will only be applied the next time the relay proxy is restarted.
//
// Flagsets are tracked by their explicit name. Flagsets without a name (or named "default")
// get a generated name at startup and therefore cannot be added or removed at runtime.
func (m *FlagsetManagerImpl) onConfigChangeWithFlagsets(newConfig *config.Config) {
	currentFlagsets := m.config.GetFlagSets()
	currentByName := indexNamedFlagsets(currentFlagsets)
	newByName := indexNamedFlagsets(newConfig.FlagSets)

	// The flagsets using enableSharedPollerPerBucket follow the top-level pollingInterval and
	// enablePollingJitter, which are not part of the flagset configuration compared by
	// reconcileExistingFlagset: they are applied to the running pollers, without rebuilding
	// the flagsets.
	// The flagsets added by this reload are left out: their retriever warns about its own polling.
	existing := make([]config.FlagSet, 0, len(newConfig.FlagSets))
	for _, fs := range newConfig.FlagSets {
		if _, current := currentByName[fs.Name]; isNamedFlagset(fs) && !current {
			continue
		}

		existing = append(existing, fs)
	}

	m.sharedRegistry.setPolling(
		time.Duration(newConfig.PollingInterval)*time.Millisecond, newConfig.EnablePollingJitter, existing)

	// 1. Remove the flagsets that are no longer in the configuration.
	for name, currentFlagset := range currentByName {
		if _, stillPresent := newByName[name]; !stillPresent {
			m.removeFlagset(currentFlagset)
		}
	}

	// 2. Add the flagsets that are new in the configuration. The shared-poller retrievers are
	// registered first so that the first listing of a bucket covers all of them.
	var added []config.FlagSet

	for name, newFlagset := range newByName {
		if _, alreadyExists := currentByName[name]; !alreadyExists {
			added = append(added, newFlagset)
		}
	}

	m.sharedRegistry.preRegisterAll(added)
	defer m.sharedRegistry.releasePending()

	for _, newFlagset := range added {
		m.addFlagset(newFlagset, newConfig.ExporterCleanQueueInterval)
	}

	// 3. Reconcile the flagsets that are present in both configurations: only their API keys
	// can be changed at runtime, every other modification is rejected.
	for name, newFlagset := range newByName {
		if currentFlagset, ok := currentByName[name]; ok {
			m.reconcileExistingFlagset(currentFlagset, newFlagset)
		}
	}

	// Rebuild the API-key -> flagset routing for every named flagset from the reconciled
	// configuration. Doing it in one pass (instead of incremental per-flagset updates) makes
	// the result independent of the order in which the flagsets above were processed, which
	// matters when an API key is moved between flagsets in a single reload.
	m.rebuildNamedAPIKeysMapping(currentByName, newByName)

	m.warnIfUnnamedFlagsetsChanged(currentFlagsets, newConfig.FlagSets)

	// The authorized keys (admin keys) and the top-level API keys are reloadable too. They are replaced
	// together with the accepted key set, in one step, so that no request sees a partial update.
	newAuthorizedKeys := newConfig.GetAuthorizedKeys()
	if !cmp.Equal(
		m.config.GetAuthorizedKeys(),
		newAuthorizedKeys,
		cmpopts.IgnoreUnexported(config.APIKeys{Admin: nil, Evaluation: nil}),
	) {
		m.logger.Info("Configuration changed: reloading the AuthorizedKeys")
	}

	m.config.ReplaceAPIKeys(newAuthorizedKeys, newConfig.GetAPIKeys())

	if newConfig.HasUnroutableEvaluationKeys() {
		m.logger.Warn(unroutableKeysWarning)
	}
}

// isNamedFlagset returns true if the flagset has an explicit, stable name that allows it to be
// managed at runtime. Flagsets without a name (or named "default") get a generated name at
// startup and cannot be added, removed or have their API keys changed at runtime.
func isNamedFlagset(flagset config.FlagSet) bool {
	return flagset.Name != "" && flagset.Name != utils.DefaultFlagSetName
}

// indexNamedFlagsets indexes the given flagsets by their name, ignoring flagsets that do not
// have an explicit name (empty or "default"): those cannot be tracked across reloads because
// they are assigned a generated name at startup.
func indexNamedFlagsets(flagsets []config.FlagSet) map[string]config.FlagSet {
	out := make(map[string]config.FlagSet, len(flagsets))
	for _, flagset := range flagsets {
		if !isNamedFlagset(flagset) {
			continue
		}

		out[flagset.Name] = flagset
	}

	return out
}

// addFlagset creates a new GoFeatureFlag client for the given flagset and registers it so it
// can serve evaluations immediately. defaultCleanQueueInterval is the top-level exporterCleanQueueInterval of the
// configuration being applied, used when the flagset does not set its own.
func (m *FlagsetManagerImpl) addFlagset(flagset config.FlagSet, defaultCleanQueueInterval time.Duration) {
	allNotifiers := appendStreamNotifiers(m.notifiers, m.sseService, m.wsService, flagset.Name)
	opts := append([]clientOption{
		withSharedBucketRegistry(m.sharedRegistry),
		withPostgresPools(m.postgresPools),
		withDefaultExporterCleanQueueInterval(defaultCleanQueueInterval),
	}, m.clientOptions...)

	client, err := newGoFeatureFlagClient(&flagset, m.logger, allNotifiers, opts...)
	if err != nil {
		m.logger.LogAttrs(context.Background(), slog.LevelError,
			"failed to create the flagset added at runtime, it will not be available until the "+
				"error is fixed",
			slog.String("flagset", flagset.Name),
			cmdlog.Err(err),
		)

		return
	}

	// Register in the authoritative config first. The API-key routing is rebuilt from the config
	// by rebuildNamedAPIKeysMapping, so a client that is not in the config would run without any
	// routing (unreachable). If the config update fails, close the client and skip rather than
	// leaving an orphan running flagset.
	addFlagSetErr := m.config.AddFlagSet(flagset)
	if addFlagSetErr != nil {
		m.logger.LogAttrs(
			context.Background(),
			slog.LevelError,
			"failed to register the new flagset in the configuration; it will not be started",
			slog.String("flagset", flagset.Name),
			cmdlog.Err(addFlagSetErr),
		)
		client.Close()

		return
	}

	// The API-key routing is (re)built in one pass by rebuildNamedAPIKeysMapping once all flagsets
	// have been reconciled.
	m.flagsetsMutex.Lock()
	m.FlagSets[flagset.Name] = client
	m.flagsetsMutex.Unlock()

	m.logger.LogAttrs(context.Background(), slog.LevelInfo, "Configuration changed: flagset added at runtime",
		slog.String("flagset", flagset.Name))
}

// removeFlagset closes the GoFeatureFlag client of the given flagset and unregisters it.
func (m *FlagsetManagerImpl) removeFlagset(flagset config.FlagSet) {
	// Stop routing to this flagset (remove its client and API keys) before closing it; the
	// remaining named flagsets' routing is rebuilt afterwards by rebuildNamedAPIKeysMapping.
	m.flagsetsMutex.Lock()
	client := m.FlagSets[flagset.Name]
	delete(m.FlagSets, flagset.Name)

	for _, apiKey := range flagset.APIKeys {
		delete(m.FlagSetRouting, apiKey)
	}
	m.flagsetsMutex.Unlock()

	// Close the client outside of the lock: Close() waits for the exporters to flush and may
	// block, we don't want to hold the lock (and block evaluation requests) while it happens.
	if client != nil {
		client.Close()
	}

	err := m.config.RemoveFlagSet(flagset.Name)
	if err != nil {
		m.logger.LogAttrs(context.Background(), slog.LevelError, "failed to remove the flagset from the configuration",
			slog.String("flagset", flagset.Name), cmdlog.Err(err))
	}

	m.logger.LogAttrs(context.Background(), slog.LevelInfo, "Configuration changed: flagset removed at runtime",
		slog.String("flagset", flagset.Name))
}

// reconcileExistingFlagset reconciles a flagset that is present in both the current and the new
// configuration. Only the API keys can be changed at runtime: any other modification is
// rejected with an error and ignored until the next restart of the relay proxy.
func (m *FlagsetManagerImpl) reconcileExistingFlagset(current, newFlagset config.FlagSet) {
	// slices.Equal is panic-safe on []string and treats nil and empty slices as equal.
	apiKeysChanged := !slices.Equal(current.APIKeys, newFlagset.APIKeys)

	// reflect.DeepEqual is used on purpose instead of cmp.Equal: it never panics on the
	// unexported fields that can be reached through the flagset configuration (e.g. the Kafka
	// exporter's *sarama.Config). Both values come from the same koanf parsing so their
	// representation is consistent.
	if !reflect.DeepEqual(current.CommonFlagSet, newFlagset.CommonFlagSet) {
		m.logger.LogAttrs(context.Background(), slog.LevelError,
			"modifying a flagset is not supported at runtime: the change is ignored and will "+
				"only be applied the next time the relay proxy is restarted. Only the API keys of a "+
				"flagset can be changed without a restart.",
			slog.String("flagset", newFlagset.Name),
		)

		if apiKeysChanged {
			m.logger.LogAttrs(context.Background(), slog.LevelError,
				"the API keys change for this flagset is also ignored because it is bundled with an "+
					"unsupported flagset modification: revert the other changes to apply the API keys "+
					"change at runtime, otherwise it will be applied on the next restart.",
				slog.String("flagset", newFlagset.Name),
			)
		}

		return
	}

	if apiKeysChanged {
		m.logger.LogAttrs(
			context.Background(),
			slog.LevelInfo,
			"Configuration changed: updating the APIKeys for flagset",
			slog.String("flagset", newFlagset.Name),
		)
		// Update only the authoritative configuration here; the routing map is rebuilt from it
		// by rebuildNamedAPIKeysMapping once all flagsets have been reconciled.
		err := m.config.SetFlagSetAPIKeys(newFlagset.Name, newFlagset.APIKeys)
		if err != nil {
			m.logger.LogAttrs(
				context.Background(),
				slog.LevelError,
				"failed to update the APIKeys for flagset",
				cmdlog.Err(err),
			)

			return
		}
	}
}

// warnIfUnnamedFlagsetsChanged logs a warning when the number of unnamed (or "default" named)
// flagsets differs between the current and the new configuration: those flagsets cannot be
// added or removed at runtime and the change requires a restart.
func (m *FlagsetManagerImpl) warnIfUnnamedFlagsetsChanged(current, newFlagsets []config.FlagSet) {
	if countUnnamedFlagsets(current) != countUnnamedFlagsets(newFlagsets) {
		m.logger.Warn(
			"adding or removing a flagset without an explicit name is not supported at runtime: " +
				"the change will only be applied the next time the relay proxy is restarted. " +
				"Give your flagsets a unique name to manage them at runtime.",
		)
	}
}

// countUnnamedFlagsets counts the flagsets that do not have an explicit name.
func countUnnamedFlagsets(flagsets []config.FlagSet) int {
	count := 0

	for _, flagset := range flagsets {
		if !isNamedFlagset(flagset) {
			count++
		}
	}

	return count
}

// rebuildNamedAPIKeysMapping rebuilds, in a single pass, the API-key -> flagset routing for
// every named flagset involved in the reload, from the reconciled (authoritative) configuration.
//
// This is done after the add/remove/reconcile passes so the resulting routing is independent of
// the order in which those passes processed the flagsets — in particular when an API key is moved
// from one flagset to another in the same reload.
//
// Routing entries that target unnamed (UUID-named) flagsets are preserved: those flagsets cannot
// be managed at runtime, so their mapping never changes here.
func (m *FlagsetManagerImpl) rebuildNamedAPIKeysMapping(currentByName, newByName map[string]config.FlagSet) {
	// Authoritative API keys per named flagset after reconciliation. Reading from the config
	// (not from newByName) preserves the previous keys of flagsets whose modification was
	// rejected.
	reconciled := indexNamedFlagsets(m.config.GetFlagSets())

	m.flagsetsMutex.Lock()
	m.dropManagedAPIKeyRouting(currentByName, newByName)
	assigned, collisions := resolveNamedAPIKeyRouting(reconciled, m.FlagSets)
	maps.Copy(m.FlagSetRouting, assigned)
	m.flagsetsMutex.Unlock()

	// Log collisions outside the lock to keep the critical section minimal (the logger sink may
	// be slow or contended).
	logAPIKeyCollisions(m.logger, collisions)
}

// dropManagedAPIKeyRouting removes every routing entry that targets a named flagset involved in
// this reload (added, removed or kept). Entries targeting unnamed flagsets are left untouched.
// The caller must hold flagsetsMutex.
func (m *FlagsetManagerImpl) dropManagedAPIKeyRouting(currentByName, newByName map[string]config.FlagSet) {
	for apiKey, target := range m.FlagSetRouting {
		_, current := currentByName[target]

		_, added := newByName[target]
		if current || added {
			delete(m.FlagSetRouting, apiKey)
		}
	}
}

// resolveNamedAPIKeyRouting computes the API-key -> flagset-name routing for the reconciled named
// flagsets that have a running client. It returns the unambiguous assignments and the keys that
// are configured on more than one flagset.
//
// A key can collide here even though newConfig.IsValid() deduped the incoming config: when an
// API-key move onto flagset A is accepted while a forbidden modification on flagset B (which still
// holds that key) is rejected, the reconciled config ends up with the same key on both A and B.
// Such a key must not route non-deterministically, so we fail closed — it is left out of the
// returned assignments (resolves to no flagset) and reported as a collision.
func resolveNamedAPIKeyRouting(
	reconciled map[string]config.FlagSet,
	running map[string]*ffclient.GoFeatureFlag,
) (map[string]string, map[string][2]string) {
	assigned := make(map[string]string)
	collisions := make(map[string][2]string)

	for name, flagset := range reconciled {
		if _, ok := running[name]; !ok {
			continue
		}

		for _, apiKey := range flagset.APIKeys {
			if prev, ok := assigned[apiKey]; ok && prev != name {
				collisions[apiKey] = [2]string{prev, name}

				continue
			}

			assigned[apiKey] = name
		}
	}

	for apiKey := range collisions {
		delete(assigned, apiKey)
	}

	return assigned, collisions
}

// logAPIKeyCollisions reports keys configured on multiple flagsets. It does not log the API key
// itself (it is a secret); it logs the flagsets the key collides between.
func logAPIKeyCollisions(logger *slog.Logger, collisions map[string][2]string) {
	for _, pair := range collisions {
		logger.LogAttrs(context.Background(), slog.LevelError,
			"an API key is configured on multiple flagsets after reconciliation; its routing is "+
				"disabled until the configuration is fixed. This can happen when an API-key move is "+
				"bundled with a rejected flagset modification.",
			slog.String("flagset", pair[0]), slog.String("otherFlagset", pair[1]),
		)
	}
}

// onConfigChangeWithDefault is called when the configuration changes in default mode.
// The only configuration that can be changed is the API Keys and the AuthorizedKeys.
// All the other configuration changes are not supported.
func (m *FlagsetManagerImpl) onConfigChangeWithDefault(newConfig *config.Config) {
	reloadAPIKeys := false
	// on default mode, we can only change the API Keys, all the other configuration changes are not supported
	// We need to read the current values with proper locking to avoid data races
	currentAuthorizedKeys := m.config.GetAuthorizedKeys()
	currentAPIKeys := m.config.GetAPIKeys()
	newAuthorizedKeys := newConfig.GetAuthorizedKeys()
	newAPIKeys := newConfig.GetAPIKeys()

	if !cmp.Equal(m.config, newConfig,
		cmpopts.IgnoreUnexported(config.Config{}), cmpopts.IgnoreFields(config.Config{}, "APIKeys", "AuthorizedKeys")) {
		m.logger.Warn("Configuration changed not supported: only API Keys and AuthorizedKeys can be " +
			"changed during runtime in default mode")
	}

	if !cmp.Equal(
		currentAuthorizedKeys,
		newAuthorizedKeys,
		cmpopts.IgnoreUnexported(config.APIKeys{Admin: nil, Evaluation: nil}),
	) {
		m.logger.Info("Configuration changed: reloading the AuthorizedKeys")
		m.config.SetAuthorizedKeys(newAuthorizedKeys)

		reloadAPIKeys = true
	}

	//nolint:staticcheck // SA1019: the deprecated setting is still supported
	if !cmp.Equal(currentAPIKeys, newAPIKeys) {
		m.logger.Info("Configuration changed: reloading the APIKeys")
		//nolint:staticcheck // SA1019: the deprecated setting is still supported
		m.config.SetAPIKeys(newAPIKeys)

		reloadAPIKeys = true
	}

	if reloadAPIKeys {
		m.config.ForceReloadAPIKeys()
	}
}

// appendStreamNotifiers returns a copy of notifiers with the SSE and websocket notifiers of
// the flagset appended, each one only when its service is non-nil.
func appendStreamNotifiers(
	notifiers []notifier.Notifier, sseService stream.SSEService, wsService stream.WebsocketService,
	flagsetName string,
) []notifier.Notifier {
	out := make([]notifier.Notifier, len(notifiers), len(notifiers)+extraNotifiers)
	copy(out, notifiers)

	if sseService != nil {
		out = append(out, proxynotifier.NewNotifierSSE(sseService, flagsetName))
	}

	if wsService != nil {
		out = append(out, proxynotifier.NewNotifierWebsocket(wsService, flagsetName))
	}

	return out
}

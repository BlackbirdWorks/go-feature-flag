package config

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/knadh/koanf/parsers/json"
	"github.com/knadh/koanf/parsers/toml/v2"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/providers/posflag"
	"github.com/knadh/koanf/v2"
	"github.com/spf13/pflag"

	cmdlog "github.com/blackbirdworks/semaphore/cmdhelpers/log"
	httpprovider "github.com/blackbirdworks/semaphore/internal/koanfhttpprovider"
	"github.com/blackbirdworks/semaphore/modules/core/legacy"
	"github.com/blackbirdworks/semaphore/utils"
)

const (
	// defaultPollingIntervalMillis is the polling interval, in milliseconds, when none is configured.
	defaultPollingIntervalMillis = 60000
)

var (
	errImpossibleToFindConfig     = errors.New("impossible to find config file")
	errImpossibleToFindConfigFile = errors.New("impossible to find config file in the default locations")
)

const (
	// EnvVariablePrefix is the prefix of the environment variables that is always read,
	// SEMAPHORE_SERVER_PORT sets server.port.
	EnvVariablePrefix = "SEMAPHORE_"
	// legacyEnvVariablePrefix is what the prefixes used with GO Feature Flag start with (GOFF_, GOFFPROXY_).
	legacyEnvVariablePrefix = "GOFF"
)

type Loader struct {
	studioParser  koanf.Parser
	studioLoadErr error
	// internal state
	k   *koanf.Koanf
	log *slog.Logger
	// input parameters
	cmdLineFlagSet *pflag.FlagSet
	fileProvider   *file.File
	stopChan       chan struct{} // signals the event processor goroutine to stop
	// environFunc returns the environment variables, os.Environ when nil (it is set by the tests).
	environFunc func() []string
	// warner is where the use of a legacy name is reported: the loader owns one that logs through log,
	// unless a LoaderOption set another.
	warner *legacy.Warner
	// flagSets loaded from studio.url, the provider and its parser are reused on reload
	studioProvider *httpprovider.HTTP
	// channel-based event processing for file watcher
	// Using a buffered channel of size 1 coalesces multiple rapid events into one
	eventChan      chan struct{} // receives file change events
	version        string
	configFilePath string // stored config file path for reload
	studioFlagSets []FlagSet
	// callbacks to be called when the configuration changes
	callbacks []func(newConfig *Config)
	// callbacksMutex is used to protect the callbacks slice
	callbacksMutex sync.RWMutex
	stopOnce       sync.Once // ensures stopChan is only closed once
	watchChanges   bool
}

// LoaderOption configures a ConfigLoader.
type LoaderOption func(*Loader)

// WithEnviron sets the source of the environment variables ([os.Environ] by default). The values
// are "KEY=value" strings, as returned by [os.Environ].
func WithEnviron(environ func() []string) LoaderOption {
	return func(c *Loader) { c.environFunc = environ }
}

// NewConfigLoader creates a new ConfigLoader.
func NewConfigLoader(
	cmdLineFlagSet *pflag.FlagSet, log *slog.Logger, version string, watchChanges bool, opts ...LoaderOption,
) *Loader {
	configLoader := Loader{
		cmdLineFlagSet: cmdLineFlagSet,
		log:            log,
		version:        version,
		watchChanges:   watchChanges,
		k:              koanf.New("."),
	}

	for _, opt := range opts {
		opt(&configLoader)
	}

	if configLoader.warner == nil {
		configLoader.warner = legacy.NewWarner(func(kind, message string) {
			log.LogAttrs(context.Background(), slog.LevelWarn, message, slog.String("legacyName", kind))
		})
	}

	// Initialize channels only if we're watching for changes
	if watchChanges {
		configLoader.eventChan = make(chan struct{}, 1) // buffered to avoid blocking
		configLoader.stopChan = make(chan struct{})
	}

	// load the configuration from the command line, the configuration file, the environment variables and the version
	configLoader.loadConfig()

	// start watching for changes
	if watchChanges {
		configLoader.startWatchChanges()
	}

	return &configLoader
}

// AddConfigChangeCallback adds a callback to be called when the configuration changes.
func (c *Loader) AddConfigChangeCallback(callback func(newConfig *Config)) {
	c.callbacksMutex.Lock()
	defer c.callbacksMutex.Unlock()

	c.callbacks = append(c.callbacks, callback)
}

// ToConfig returns the configuration.
func (c *Loader) ToConfig() (*Config, error) {
	proxyConf := &Config{}

	errUnmarshal := c.k.Unmarshal("", proxyConf)
	if errUnmarshal != nil {
		return nil, fmt.Errorf("unmarshal config: %w", errUnmarshal)
	}

	switch {
	case len(proxyConf.FlagSets) > 0:
		// IsValid rejects local flagSets when studio is also set
		proxyConf.flagSetMode = flagSetModeLocal
	case proxyConf.Studio.URL != "":
		proxyConf.flagSetMode = flagSetModeStudio
		proxyConf.FlagSets = c.studioFlagSets
	default:
		proxyConf.flagSetMode = flagSetModeNone
	}

	processExporters(proxyConf)

	return proxyConf, nil
}

// StopWatchChanges stops watching for changes in the configuration file and Studio.
func (c *Loader) StopWatchChanges() error {
	if (c.fileProvider == nil && c.studioProvider == nil) || !c.watchChanges {
		return nil
	}

	// Signal the event processor goroutine to stop (only once)
	if c.stopChan != nil {
		c.stopOnce.Do(func() {
			close(c.stopChan)
		})
	}

	var errs []error
	if c.fileProvider != nil {
		errs = append(errs, c.fileProvider.Unwatch())
	}

	if c.studioProvider != nil {
		errs = append(errs, c.studioProvider.Unwatch())
	}

	return errors.Join(errs...)
}

// loadConfig loads the configuration from
// - the command line
// - the configuration file
// - the environment variables
// - the flagSets from Studio.
func (c *Loader) loadConfig() {
	_ = c.k.Load(confmap.Provider(map[string]any{
		"fileFormat":      "yaml",
		"pollingInterval": defaultPollingIntervalMillis,
		"logLevel":        DefaultLogLevel,
	}, "."), nil)

	c.loadPosflag(c.cmdLineFlagSet)
	c.loadConfigFile()
	c.loadEnvVariables()
	c.loadStudioFlagSets()
	_ = c.k.Set("version", c.version)
}

// startWatchChanges starts watching for changes in the configuration file and Studio.
func (c *Loader) startWatchChanges() {
	if (c.fileProvider == nil && c.studioProvider == nil) || !c.watchChanges {
		return
	}

	// Start the event processor goroutine
	// This approach is used to avoid blocking the fsnotify goroutine
	// The event processor goroutine is responsible for processing the events and reloading the configuration
	// Check https://github.com/knadh/koanf/issues/12#issuecomment-2637665148 to understand why this is necessary
	go c.processConfigChangeEvents()

	// The watcher callback just sends to the channel and returns immediately
	// This prevents blocking the fsnotify goroutine
	onChange := func(_ any, err error) {
		if err != nil {
			c.log.LogAttrs(
				context.Background(),
				slog.LevelError,
				"error watching for configuration changes",
				cmdlog.Err(err),
			)

			return
		}

		// Non-blocking send to the event channel
		// If the channel is full (event already pending), we skip this event
		// since the pending event will trigger a config reload anyway
		select {
		case c.eventChan <- struct{}{}:
			// Event sent successfully
		default:
			// Channel full, event already pending - skip this one
		}
	}
	if c.fileProvider != nil {
		errWatch := c.fileProvider.Watch(onChange)
		if errWatch != nil {
			c.log.LogAttrs(
				context.Background(),
				slog.LevelError,
				"error watching for configuration changes (error from file provider)",
				cmdlog.Err(errWatch),
			)
		}
	}

	if c.studioProvider != nil {
		errWatch := c.studioProvider.Watch(onChange)
		if errWatch != nil {
			c.log.LogAttrs(
				context.Background(),
				slog.LevelError,
				"error watching for studio flagSets changes",
				cmdlog.Err(errWatch),
			)
		}
	}
}

// processConfigChangeEvents processes file change events from the channel.
// The buffered channel (size 1) naturally coalesces rapid events - if an event
// is already pending, new events are dropped since they would trigger the same reload.
// A small debounce delay is added to handle file systems (especially Linux with inotify)
// where [os.WriteFile] triggers multiple events (truncate + write) in rapid succession.
func (c *Loader) processConfigChangeEvents() {
	// debounceDelay gives time for rapid file system events to settle.
	// On Linux, os.WriteFile can trigger: 1) truncate event (file empty) 2) write event (file has content)
	// Without debounce, we might read the file between truncate and write, seeing empty content.
	const debounceDelay = 50 * time.Millisecond

	for {
		select {
		case <-c.stopChan:
			return
		case <-c.eventChan:
			// Debounce: wait a short time and drain any additional events that arrive
			// This allows multiple rapid fsnotify events to coalesce into one reload
		drainLoop:
			for {
				select {
				case <-c.stopChan:
					return
				case <-c.eventChan:
					// Another event arrived, keep draining
				case <-time.After(debounceDelay):
					// No more events for debounceDelay, proceed with reload
					break drainLoop
				}
			}

			c.reloadConfigAndNotify()
		}
	}
}

// reloadConfigAndNotify reloads the configuration and notifies all callbacks.
func (c *Loader) reloadConfigAndNotify() {
	// Create a new ConfigLoader with the stored config file path
	newConfig := &Loader{
		cmdLineFlagSet: c.cmdLineFlagSet,
		log:            c.log,
		version:        c.version,
		watchChanges:   false,
		k:              koanf.New("."),
		configFilePath: c.configFilePath,
		// Reuse the provider's ETag and cache across reloads.
		studioProvider: c.studioProvider,
		studioParser:   c.studioParser,
	}
	newConfig.loadConfig()

	if newConfig.studioLoadErr != nil {
		return
	}

	modifiedConfig, err := newConfig.ToConfig()
	if err != nil {
		c.log.LogAttrs(context.Background(), slog.LevelError, "error loading new config", cmdlog.Err(err))

		return
	}

	c.callbacksMutex.RLock()
	defer c.callbacksMutex.RUnlock()

	for _, callback := range c.callbacks {
		callback(modifiedConfig)
	}
}

// loadPosflag loads the flags from the command line.
func (c *Loader) loadPosflag(cmdLineFlagSet *pflag.FlagSet) {
	posflagProvider := posflag.Provider(cmdLineFlagSet, ".", c.k)

	errBindFlag := c.k.Load(posflagProvider, nil)
	if errBindFlag != nil {
		c.log.LogAttrs(
			context.Background(),
			slog.LevelError,
			"impossible to parse flag command line",
			cmdlog.Err(errBindFlag),
		)
	}
}

// loadConfigFile loads the configuration file.
func (c *Loader) loadConfigFile() {
	var errFileLocation error

	// Use stored config file path if available (for reload), otherwise read from flag set
	if c.configFilePath == "" {
		c.configFilePath, errFileLocation = locateConfigFile(c.k.String("config"), c.legacyWarner())
		if errFileLocation != nil {
			c.log.LogAttrs(
				context.Background(),
				slog.LevelError,
				"not using any configuration file",
				cmdlog.Err(errFileLocation),
			)

			return
		}
	}

	parser := selectParserForFile(c.configFilePath)
	c.fileProvider = file.Provider(c.configFilePath)

	errBindFile := c.k.Load(c.fileProvider, parser)
	if errBindFile != nil {
		c.log.LogAttrs(context.Background(), slog.LevelError, "error loading file", cmdlog.Err(errBindFile))
	}
}

// loadEnvVariables loads the environment variables.
//
// The variables are read with the prefix of the envVariablePrefix setting (no prefix by default), then with
// the SEMAPHORE_ prefix, which is always accepted. A variable with the SEMAPHORE_ prefix wins over the same
// variable without it.
func (c *Loader) loadEnvVariables() {
	prefix := c.k.String("envVariablePrefix")
	if strings.HasPrefix(strings.ToUpper(prefix), legacyEnvVariablePrefix) {
		c.legacyWarner().Warn(legacy.KindEnvPrefix, fmt.Sprintf(
			"envVariablePrefix %q is a legacy GO Feature Flag prefix, it still works, "+
				"%s is the Semaphore prefix and is always accepted", prefix, EnvVariablePrefix))
	}

	_ = c.k.Load(c.mapEnvVariablesProvider(prefix, c.log), nil)
	if prefix != EnvVariablePrefix {
		_ = c.k.Load(c.mapEnvVariablesProvider(EnvVariablePrefix, c.log), nil)
	}
}

// legacyWarner returns where the use of a legacy name is reported.
func (c *Loader) legacyWarner() *legacy.Warner {
	return c.warner
}

// loadStudioFlagSets loads the flagSets from studio.url, if it is set.
func (c *Loader) loadStudioFlagSets() {
	c.studioLoadErr = nil
	if c.studioProvider == nil {
		conf, err := c.ToConfig()
		if err != nil || conf.Studio.URL == "" {
			return
		}

		c.studioParser = conf.Studio.parser()
		c.studioProvider = conf.Studio.provider(c.studioParser, c.log)
	}

	studio := koanf.New(".")

	var studioConf struct {
		FlagSets []FlagSet `koanf:"flagsets"`
	}

	err := studio.Load(c.studioProvider, c.studioParser)
	if err == nil {
		err = studio.Unmarshal("", &studioConf)
	}

	if err == nil {
		err = (&Config{FlagSets: studioConf.FlagSets}).validateFlagSets()
	}

	if err != nil {
		// Polling may already have accepted this document. Retry it even if its
		// ETag and content are unchanged, since this reload could not use it.
		c.studioProvider.Invalidate()
		c.studioLoadErr = err
		c.log.LogAttrs(context.Background(), slog.LevelError, "error loading the flagSets from studio", cmdlog.Err(err))

		return
	}

	c.studioFlagSets = studioConf.FlagSets
}

// Names of the relay proxy configuration file, without extension.
const (
	configFileName       = "semaphore-proxy"
	legacyConfigFileName = "goff-proxy"
)

// configFileLocations are the folders searched for the configuration file when --config is not set.
// The /goff/ folders are the ones of GO Feature Flag, they are searched after the Semaphore ones.
func configFileLocations() []string {
	return []string{
		"./", "/semaphore/", "/etc/opt/semaphore/",
		"/goff/", "/etc/opt/goff/",
	}
}

// locateConfigFile is selecting the configuration file we will use.
// In every location semaphore-proxy.<ext> is searched before the legacy goff-proxy.<ext>.
func locateConfigFile(inputFilePath string, warner *legacy.Warner) (string, error) {
	return locateConfigFileIn(inputFilePath, configFileLocations(), warner)
}

// locateConfigFileIn is locateConfigFile with the folders to search.
func locateConfigFileIn(inputFilePath string, locations []string, warner *legacy.Warner) (string, error) {
	supportedExtensions := []string{"yaml", "toml", "json", "yml"}

	if inputFilePath != "" {
		_, err := os.Stat(inputFilePath)
		if err != nil {
			return "", fmt.Errorf("%w %s", errImpossibleToFindConfig, inputFilePath)
		}

		return inputFilePath, nil
	}

	for _, location := range locations {
		for _, filename := range []string{configFileName, legacyConfigFileName} {
			for _, ext := range supportedExtensions {
				configFile := fmt.Sprintf("%s%s.%s", location, filename, ext)

				_, err := os.Stat(configFile)
				if err != nil {
					continue
				}

				if filename == legacyConfigFileName {
					warner.Warn(legacy.KindConfigFile, fmt.Sprintf(
						"the configuration file %s uses the legacy name %s, it is still accepted, "+
							"rename it to %s.%s", configFile, legacyConfigFileName, configFileName, ext))
				}

				return configFile, nil
			}
		}
	}

	return "", fmt.Errorf("%w [%s]", errImpossibleToFindConfigFile,
		strings.Join(locations, ","),
	)
}

// selectParserForFile returns the appropriate parser based on file extension.
//
//nolint:ireturn // selects among the toml, json and yaml parsers at runtime
func selectParserForFile(configFileLocation string) koanf.Parser {
	ext := filepath.Ext(configFileLocation)
	switch strings.ToLower(ext) {
	case ".toml":
		return toml.Parser()
	case ".json":
		return json.Parser()
	default:
		return yaml.Parser()
	}
}

// processExporters handles the post-processing of exporters configuration.
func processExporters(proxyConf *Config) {
	if proxyConf.Exporters == nil {
		return
	}

	for i := range *proxyConf.Exporters {
		addresses := (*proxyConf.Exporters)[i].Kafka.Addresses
		if len(addresses) == 0 || (len(addresses) == 1 && strings.Contains(addresses[0], ",")) {
			(*proxyConf.Exporters)[i].Kafka.Addresses = utils.StringToArray(addresses)
		}
	}
}

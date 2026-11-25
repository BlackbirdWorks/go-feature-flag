package config_test

import (
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	"github.com/blackbirdworks/semaphore/testhelpers"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
	"github.com/blackbirdworks/semaphore/testhelpers/slogutil"
)

const (
	configFlag                    = "--config="
	serverPort1031PollingInterval = `server:
  port: 1031
pollingInterval: 1000
loglevel: info`
	timeoutWaitingForCallback = "Timeout waiting for callback to be triggered"
)

func TestConfigLoaderAddConfigChangeCallback(t *testing.T) {
	t.Parallel()

	tests := []struct {
		setupCallback func(*testing.T, *config.Loader) (*[]*config.Config, *sync.Mutex)
		name          string
		initialConfig string
		updatedConfig string
		wantCallbacks int
	}{
		{
			name:          "Single callback triggered on config change",
			initialConfig: serverPort1031PollingInterval,
			updatedConfig: `server:
  port: 2031
pollingInterval: 2000
loglevel: debug`,
			wantCallbacks: 1,
			setupCallback: func(t *testing.T, loader *config.Loader) (*[]*config.Config, *sync.Mutex) {
				t.Helper()

				var mu sync.Mutex

				receivedConfigs := make([]*config.Config, 0)

				loader.AddConfigChangeCallback(func(newConfig *config.Config) {
					mu.Lock()
					defer mu.Unlock()

					receivedConfigs = append(receivedConfigs, newConfig)
				})

				return &receivedConfigs, &mu
			},
		},
		{
			name:          "Multiple callbacks triggered on config change",
			initialConfig: serverPort1031PollingInterval,
			updatedConfig: `server:
  port: 3031
pollingInterval: 3000
loglevel: warn`,
			wantCallbacks: 2,
			setupCallback: func(t *testing.T, loader *config.Loader) (*[]*config.Config, *sync.Mutex) {
				t.Helper()

				var mu sync.Mutex

				receivedConfigs := make([]*config.Config, 0)

				loader.AddConfigChangeCallback(func(newConfig *config.Config) {
					mu.Lock()
					defer mu.Unlock()

					receivedConfigs = append(receivedConfigs, newConfig)
				})

				loader.AddConfigChangeCallback(func(newConfig *config.Config) {
					mu.Lock()
					defer mu.Unlock()

					receivedConfigs = append(receivedConfigs, newConfig)
				})

				return &receivedConfigs, &mu
			},
		},
		{
			name:          "Callback receives updated config values",
			initialConfig: serverPort1031PollingInterval,
			updatedConfig: `server:
  port: 9999
pollingInterval: 5000
loglevel: error`,
			wantCallbacks: 1,
			setupCallback: func(t *testing.T, loader *config.Loader) (*[]*config.Config, *sync.Mutex) {
				t.Helper()

				var mu sync.Mutex

				receivedConfigs := make([]*config.Config, 0)

				loader.AddConfigChangeCallback(func(newConfig *config.Config) {
					mu.Lock()
					defer mu.Unlock()

					receivedConfigs = append(receivedConfigs, newConfig)
				})

				return &receivedConfigs, &mu
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Create temporary directory and config file
			configFile := testhelpers.CopyContentToNewTempFile(t, tt.initialConfig)
			// Create flag set and parse config file path
			flagSet := pflag.NewFlagSet(fixtures.ConfigFlagName, pflag.ContinueOnError)
			flagSet.String(fixtures.ConfigFlagName, "", locationOfYourConfig)
			err := flagSet.Parse([]string{configFlag + configFile.Name()})
			require.NoError(t, err)

			// Create ConfigLoader with watchChanges enabled
			logger := slog.New(slog.DiscardHandler)

			loader := config.NewConfigLoader(flagSet, logger, fixtures.VersionOneZeroZero, true)
			defer func() { require.NoError(t, loader.StopWatchChanges()) }()

			// Setup callbacks
			receivedConfigs, mu := tt.setupCallback(t, loader)

			// Get initial config to verify it's loaded correctly
			initialConfig, err := loader.ToConfig()
			require.NoError(t, err)
			assert.Equal(t, 1031, initialConfig.Server.Port)

			// the watch of the file is registered when NewConfigLoader returns: no need to wait for it

			// Update config file to trigger callback
			_ = testhelpers.CopyContentToExistingTempFile(t, tt.updatedConfig, configFile)

			// Wait for callback to be triggered (file watchers may have some delay)
			timeout := time.After(5 * time.Second)

			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()

			for {
				select {
				case <-timeout:
					require.Fail(t, timeoutWaitingForCallback)
				case <-ticker.C:
					mu.Lock()
					callbackCount := len(*receivedConfigs)
					mu.Unlock()

					if callbackCount >= tt.wantCallbacks {
						// Verify callback was called with correct config
						mu.Lock()

						assert.Len(
							t,
							*receivedConfigs,
							tt.wantCallbacks,
							"Expected %d callbacks, got %d",
							tt.wantCallbacks,
							callbackCount,
						)

						// Verify the last received config has updated values
						lastConfig := (*receivedConfigs)[len(*receivedConfigs)-1]
						// Verify that the config changed from initial values
						assert.NotEqual(t, 1031, lastConfig.Server.Port, "Port should have changed from initial value")
						assert.NotEqual(
							t,
							1000,
							lastConfig.PollingInterval,
							"PollingInterval should have changed from initial value",
						)
						assert.NotEqual(
							t,
							fixtures.LogLevelInfo,
							lastConfig.LogLevel,
							"LogLevel should have changed from initial value",
						)

						mu.Unlock()

						// Clean up
						err = loader.StopWatchChanges()
						assert.NoError(t, err)

						return
					}
				}
			}
		})
	}
}

func TestConfigLoaderAddConfigChangeCallbackNoWatchChanges(t *testing.T) {
	t.Parallel()

	configFile := testhelpers.CopyContentToNewTempFile(t, serverPort1031PollingInterval)

	// Create flag set and parse config file path
	flagSet := pflag.NewFlagSet(fixtures.ConfigFlagName, pflag.ContinueOnError)
	flagSet.String(fixtures.ConfigFlagName, "", locationOfYourConfig)
	err := flagSet.Parse([]string{configFlag + configFile.Name()})
	require.NoError(t, err)

	// Create ConfigLoader with watchChanges disabled
	logger := slog.New(slog.DiscardHandler)
	loader := config.NewConfigLoader(flagSet, logger, fixtures.VersionOneZeroZero, false)

	// Setup callback
	var (
		mu              sync.Mutex
		receivedConfigs []*config.Config
	)

	loader.AddConfigChangeCallback(func(newConfig *config.Config) {
		mu.Lock()
		defer mu.Unlock()

		receivedConfigs = append(receivedConfigs, newConfig)
	})

	// Update config file
	_ = testhelpers.CopyContentToExistingTempFile(t, `server:
  port: 9999
pollingInterval: 5000
loglevel: debug`, configFile)

	// Wait a bit to ensure no callback is triggered
	time.Sleep(1 * time.Second)

	// Verify callback was NOT called
	mu.Lock()
	defer mu.Unlock()

	assert.Empty(t, receivedConfigs, "Callback should not be triggered when watchChanges is disabled")
}

func TestConfigLoaderAddConfigChangeCallbackInvalidConfig(t *testing.T) {
	t.Parallel()

	// Create temporary directory and config file
	configFile := testhelpers.CopyContentToNewTempFile(t, serverPort1031PollingInterval)

	// Create flag set and parse config file path
	flagSet := pflag.NewFlagSet(fixtures.ConfigFlagName, pflag.ContinueOnError)
	flagSet.String(fixtures.ConfigFlagName, "", locationOfYourConfig)
	err := flagSet.Parse([]string{configFlag + configFile.Name()})
	require.NoError(t, err)

	// Create ConfigLoader with watchChanges enabled and observer logger to capture logs
	core, logs := slogutil.NewObserver(slog.LevelError)
	logger := core
	loader := config.NewConfigLoader(flagSet, logger, fixtures.VersionOneZeroZero, true)

	// Setup callback
	var (
		mu             sync.Mutex
		callbackCalled bool
	)

	loader.AddConfigChangeCallback(func(_ *config.Config) {
		mu.Lock()
		defer mu.Unlock()

		callbackCalled = true
	})

	// Write invalid config file
	invalidConfig := `server:
  port: invalid
pollingInterval: 1000
loglevel: info`
	_ = testhelpers.CopyContentToExistingTempFile(t, invalidConfig, configFile)

	// Wait a bit for the file watcher to detect the change and attempt to load the config
	time.Sleep(1 * time.Second)

	// Verify callback was NOT called due to invalid config
	mu.Lock()
	callbackNotCalled := !callbackCalled
	mu.Unlock()
	assert.True(t, callbackNotCalled, "Callback should not be triggered when new config is invalid")

	// Verify that an error log was written
	assert.GreaterOrEqual(t, logs.Len(), 1, "Expected at least one error log when config is invalid")
	errorLogs := logs.FilterMessage("error loading new config")
	assert.GreaterOrEqual(t, errorLogs.Len(), 1, "Expected error log with message 'error loading new config'")

	// Clean up
	err = loader.StopWatchChanges()
	assert.NoError(t, err)
}
